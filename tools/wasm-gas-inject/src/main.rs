use std::env;
use std::fs;
use wasmparser::{Operator, Parser, Payload, TypeRef, ValType};
use wasm_encoder::{
    CodeSection, EntityType, Function, ImportSection, Instruction, Module, RawSection,
    reencode::{Reencode, RoundtripReencoder},
};

const GAS_MODULE: &str = "env";
const GAS_FUNC: &str = "gas";

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() != 3 {
        eprintln!("Usage: wasm-gas-inject <input.wasm> <output.wasm>");
        std::process::exit(1);
    }

    let wasm = fs::read(&args[1]).expect("read input");
    eprintln!("Input: {} bytes", wasm.len());

    match inject_gas(&wasm) {
        Ok(out) => {
            eprintln!("Output: {} bytes", out.len());
            fs::write(&args[2], &out).expect("write output");
        }
        Err(e) => {
            eprintln!("Error: {}", e);
            std::process::exit(1);
        }
    }
}

fn inject_gas(wasm: &[u8]) -> Result<Vec<u8>, String> {
    // First pass: count types and function imports.
    let mut num_types: u32 = 0;
    let mut num_func_imports: u32 = 0;

    for payload in Parser::new(0).parse_all(wasm) {
        let payload = payload.map_err(|e| e.to_string())?;
        match &payload {
            Payload::TypeSection(r) => num_types = r.count(),
            Payload::ImportSection(r) => {
                for imp in r.clone() {
                    let imp = imp.map_err(|e| e.to_string())?;
                    if matches!(imp.ty, TypeRef::Func(_)) {
                        num_func_imports += 1;
                    }
                }
            }
            _ => {}
        }
    }

    // Gas import is inserted as the FIRST import.
    // gas_func_idx = 0, all existing function indices shift by +1.
    let gas_func_idx: u32 = 0;
    let gas_type_idx: u32 = num_types; // appended after existing types

    eprintln!("  types={}, func_imports={}, gas_type={}, gas_func={}",
              num_types, num_func_imports, gas_type_idx, gas_func_idx);

    // Collect code bodies for instrumentation.
    let mut code_bodies: Vec<wasmparser::FunctionBody> = Vec::new();
    for payload in Parser::new(0).parse_all(wasm) {
        if let Ok(Payload::CodeSectionEntry(body)) = payload {
            code_bodies.push(body);
        }
    }

    // Second pass: rebuild module with gas injection.
    let mut module = Module::new();
    let mut code_emitted = false;

    for payload in Parser::new(0).parse_all(wasm) {
        let payload = payload.map_err(|e| e.to_string())?;

        match &payload {
            Payload::TypeSection(reader) => {
                let mut ts = wasm_encoder::TypeSection::new();
                for rec in reader.clone() {
                    let rec = rec.map_err(|e| e.to_string())?;
                    for st in rec.into_types() {
                        if let wasmparser::CompositeInnerType::Func(ft) = &st.composite_type.inner {
                            let params: Vec<_> = ft.params().iter().map(|v| conv_vt(*v)).collect();
                            let results: Vec<_> = ft.results().iter().map(|v| conv_vt(*v)).collect();
                            ts.ty().function(params, results);
                        }
                    }
                }
                // Append gas type: (i64) -> ()
                ts.ty().function(vec![wasm_encoder::ValType::I64], vec![]);
                module.section(&ts);
            }

            Payload::ImportSection(reader) => {
                let mut is = ImportSection::new();
                // Gas import FIRST.
                is.import(GAS_MODULE, GAS_FUNC, EntityType::Function(gas_type_idx));
                // Existing imports.
                for imp in reader.clone() {
                    let imp = imp.map_err(|e| e.to_string())?;
                    let ty = match imp.ty {
                        TypeRef::Func(idx) => EntityType::Function(idx),
                        TypeRef::Table(t) => EntityType::Table(conv_table(t)),
                        TypeRef::Memory(m) => EntityType::Memory(conv_mem(m)),
                        TypeRef::Global(g) => EntityType::Global(conv_global(g)),
                        TypeRef::Tag(t) => EntityType::Tag(wasm_encoder::TagType {
                            kind: wasm_encoder::TagKind::Exception,
                            func_type_idx: t.func_type_idx,
                        }),
                    };
                    is.import(imp.module, imp.name, ty);
                }
                module.section(&is);
            }

            Payload::CodeSectionStart { .. } | Payload::CodeSectionEntry(_) => {
                if !code_emitted {
                    code_emitted = true;
                    let cs = instrument_code(&code_bodies, gas_func_idx)?;
                    module.section(&cs);
                }
            }

            Payload::End(_) => {}

            _ => {
                if let Some((id, range)) = payload.as_section() {
                    // Skip sections we rebuild.
                    if id == 1 || id == 2 || id == 10 { continue; }

                    // Export section (id=7): needs function index remapping.
                    if id == 7 {
                        remap_export_section(&mut module, wasm);
                        continue;
                    }

                    // Element section (id=9): needs function index remapping.
                    if id == 9 {
                        remap_element_section(&mut module, wasm);
                        continue;
                    }

                    // Global section (id=6): reject funcref globals.
                    if id == 6 {
                        validate_globals(wasm)?;
                    }

                    module.section(&RawSection { id, data: &wasm[range] });
                }
            }
        }
    }

    let output = module.finish();
    validate_output(&output)?;
    Ok(output)
}

/// Instrument all code bodies: inject gas charge at function entry, remap calls.
fn instrument_code(
    bodies: &[wasmparser::FunctionBody],
    gas_func_idx: u32,
) -> Result<CodeSection, String> {
    let mut cs = CodeSection::new();
    let mut reencoder = RoundtripReencoder;
    let mut total_gas_points = 0u64;

    for body in bodies {
        let locals_reader = body.get_locals_reader().map_err(|e| e.to_string())?;
        let ops_reader = body.get_operators_reader().map_err(|e| e.to_string())?;

        // Parse locals.
        let locals: Vec<(u32, wasm_encoder::ValType)> = locals_reader
            .into_iter()
            .map(|l| {
                let l = l.unwrap();
                (l.0, conv_vt(l.1))
            })
            .collect();

        let mut func = Function::new(locals);

        // Parse all operators.
        let operators: Vec<Operator> = ops_reader
            .into_iter()
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?;

        let op_count = operators.len() as i64;

        // Inject gas charge at function entry.
        func.instruction(&Instruction::I64Const(op_count));
        func.instruction(&Instruction::Call(gas_func_idx));
        total_gas_points += 1;

        // Re-encode all operators with function index remapping (+1).
        for op in &operators {
            // Allowlist: only permit opcodes we fully understand and handle.
            if !is_allowed_op(op) {
                return Err(format!("disallowed opcode: {:?}", op));
            }

            match op {
                // Remap function calls: shift index by +1.
                Operator::Call { function_index } => {
                    func.instruction(&Instruction::Call(function_index + 1));
                }
                Operator::ReturnCall { function_index } => {
                    func.instruction(&Instruction::ReturnCall(function_index + 1));
                }
                Operator::RefFunc { function_index } => {
                    func.instruction(&Instruction::RefFunc(function_index + 1));
                }
                // Loop: inject gas charge before loop body.
                Operator::Loop { blockty } => {
                    let bt = reencoder.block_type(*blockty).map_err(|e| e.to_string())?;
                    func.instruction(&Instruction::Loop(bt));
                    // Charge gas at loop entry (cost = 1 per iteration as a baseline).
                    func.instruction(&Instruction::I64Const(1));
                    func.instruction(&Instruction::Call(gas_func_idx));
                    total_gas_points += 1;
                }
                // All other operators: reencode as-is.
                other => {
                    let inst = reencoder.instruction(other.clone()).map_err(|e: wasm_encoder::reencode::Error<std::convert::Infallible>| e.to_string())?;
                    func.instruction(&inst);
                }
            }
        }

        cs.function(&func);
    }

    eprintln!("  instrumented {} functions, {} gas points", bodies.len(), total_gas_points);
    Ok(cs)
}

/// Remap export section: shift function indices by +1.
fn remap_export_section(module: &mut Module, wasm: &[u8]) {
    let mut es = wasm_encoder::ExportSection::new();
    for payload in Parser::new(0).parse_all(wasm) {
        if let Ok(Payload::ExportSection(reader)) = payload {
            for export in reader {
                let export = export.unwrap();
                match export.kind {
                    wasmparser::ExternalKind::Func => {
                        es.export(export.name, wasm_encoder::ExportKind::Func, export.index + 1);
                    }
                    wasmparser::ExternalKind::Table => {
                        es.export(export.name, wasm_encoder::ExportKind::Table, export.index);
                    }
                    wasmparser::ExternalKind::Memory => {
                        es.export(export.name, wasm_encoder::ExportKind::Memory, export.index);
                    }
                    wasmparser::ExternalKind::Global => {
                        es.export(export.name, wasm_encoder::ExportKind::Global, export.index);
                    }
                    wasmparser::ExternalKind::Tag => {
                        es.export(export.name, wasm_encoder::ExportKind::Tag, export.index);
                    }
                }
            }
        }
    }
    module.section(&es);
}

/// Remap element section: shift function indices by +1.
fn remap_element_section(module: &mut Module, wasm: &[u8]) {
    let mut es = wasm_encoder::ElementSection::new();
    let mut reencoder = RoundtripReencoder;

    for payload in Parser::new(0).parse_all(wasm) {
        if let Ok(Payload::ElementSection(reader)) = payload {
            for elem in reader {
                let elem = elem.unwrap();
                // TinyGo only uses active element segments with function indices.
                let wasmparser::ElementKind::Active { table_index, offset_expr } = elem.kind else {
                    panic!("unsupported element kind (only active supported)");
                };
                let wasmparser::ElementItems::Functions(funcs) = elem.items else {
                    panic!("unsupported element items (only function indices supported)");
                };
                let offset = reencoder.const_expr(offset_expr).unwrap();
                let indices: Vec<u32> = funcs.into_iter()
                    .map(|f| f.unwrap() + 1)
                    .collect();
                es.active(
                    table_index,
                    &offset,
                    wasm_encoder::Elements::Functions(std::borrow::Cow::Borrowed(&indices)),
                );
            }
        }
    }
    module.section(&es);
}

/// Allowlist of WASM opcodes we fully handle in the gas injector.
/// Anything not on this list is rejected at deploy time.
fn is_allowed_op(op: &Operator) -> bool {
    matches!(op,
        // Control flow
        Operator::Unreachable |
        Operator::Nop |
        Operator::Block { .. } |
        Operator::Loop { .. } |
        Operator::If { .. } |
        Operator::Else |
        Operator::End |
        Operator::Br { .. } |
        Operator::BrIf { .. } |
        Operator::BrTable { .. } |
        Operator::Return |
        Operator::Call { .. } |         // remapped +1
        Operator::CallIndirect { .. } | // uses type index, safe
        Operator::ReturnCall { .. } |   // remapped +1

        // Parametric
        Operator::Drop |
        Operator::Select |

        // Variable
        Operator::LocalGet { .. } |
        Operator::LocalSet { .. } |
        Operator::LocalTee { .. } |
        Operator::GlobalGet { .. } |
        Operator::GlobalSet { .. } |

        // Memory
        Operator::I32Load { .. } |
        Operator::I64Load { .. } |
        Operator::F32Load { .. } |
        Operator::F64Load { .. } |
        Operator::I32Load8S { .. } |
        Operator::I32Load8U { .. } |
        Operator::I32Load16S { .. } |
        Operator::I32Load16U { .. } |
        Operator::I64Load8S { .. } |
        Operator::I64Load8U { .. } |
        Operator::I64Load16S { .. } |
        Operator::I64Load16U { .. } |
        Operator::I64Load32S { .. } |
        Operator::I64Load32U { .. } |
        Operator::I32Store { .. } |
        Operator::I64Store { .. } |
        Operator::F32Store { .. } |
        Operator::F64Store { .. } |
        Operator::I32Store8 { .. } |
        Operator::I32Store16 { .. } |
        Operator::I64Store8 { .. } |
        Operator::I64Store16 { .. } |
        Operator::I64Store32 { .. } |
        Operator::MemorySize { .. } |
        Operator::MemoryGrow { .. } |
        Operator::MemoryCopy { .. } |   // bulk memory — TinyGo uses this
        Operator::MemoryFill { .. } |   // bulk memory — TinyGo uses this

        // Constants
        Operator::I32Const { .. } |
        Operator::I64Const { .. } |
        Operator::F32Const { .. } |
        Operator::F64Const { .. } |

        // i32 arithmetic
        Operator::I32Eqz |
        Operator::I32Eq |
        Operator::I32Ne |
        Operator::I32LtS |
        Operator::I32LtU |
        Operator::I32GtS |
        Operator::I32GtU |
        Operator::I32LeS |
        Operator::I32LeU |
        Operator::I32GeS |
        Operator::I32GeU |
        Operator::I32Clz |
        Operator::I32Ctz |
        Operator::I32Popcnt |
        Operator::I32Add |
        Operator::I32Sub |
        Operator::I32Mul |
        Operator::I32DivS |
        Operator::I32DivU |
        Operator::I32RemS |
        Operator::I32RemU |
        Operator::I32And |
        Operator::I32Or |
        Operator::I32Xor |
        Operator::I32Shl |
        Operator::I32ShrS |
        Operator::I32ShrU |
        Operator::I32Rotl |
        Operator::I32Rotr |

        // i64 arithmetic
        Operator::I64Eqz |
        Operator::I64Eq |
        Operator::I64Ne |
        Operator::I64LtS |
        Operator::I64LtU |
        Operator::I64GtS |
        Operator::I64GtU |
        Operator::I64LeS |
        Operator::I64LeU |
        Operator::I64GeS |
        Operator::I64GeU |
        Operator::I64Clz |
        Operator::I64Ctz |
        Operator::I64Popcnt |
        Operator::I64Add |
        Operator::I64Sub |
        Operator::I64Mul |
        Operator::I64DivS |
        Operator::I64DivU |
        Operator::I64RemS |
        Operator::I64RemU |
        Operator::I64And |
        Operator::I64Or |
        Operator::I64Xor |
        Operator::I64Shl |
        Operator::I64ShrS |
        Operator::I64ShrU |
        Operator::I64Rotl |
        Operator::I64Rotr |

        // Conversions
        Operator::I32WrapI64 |
        Operator::I64ExtendI32S |
        Operator::I64ExtendI32U |
        Operator::I32Extend8S |
        Operator::I32Extend16S |
        Operator::I64Extend8S |
        Operator::I64Extend16S |
        Operator::I64Extend32S |

        // f32/f64 (TinyGo may use for float math)
        Operator::F32Eq |
        Operator::F32Ne |
        Operator::F32Lt |
        Operator::F32Gt |
        Operator::F32Le |
        Operator::F32Ge |
        Operator::F64Eq |
        Operator::F64Ne |
        Operator::F64Lt |
        Operator::F64Gt |
        Operator::F64Le |
        Operator::F64Ge |
        Operator::F32Abs |
        Operator::F32Neg |
        Operator::F32Ceil |
        Operator::F32Floor |
        Operator::F32Trunc |
        Operator::F32Nearest |
        Operator::F32Sqrt |
        Operator::F32Add |
        Operator::F32Sub |
        Operator::F32Mul |
        Operator::F32Div |
        Operator::F32Min |
        Operator::F32Max |
        Operator::F32Copysign |
        Operator::F64Abs |
        Operator::F64Neg |
        Operator::F64Ceil |
        Operator::F64Floor |
        Operator::F64Trunc |
        Operator::F64Nearest |
        Operator::F64Sqrt |
        Operator::F64Add |
        Operator::F64Sub |
        Operator::F64Mul |
        Operator::F64Div |
        Operator::F64Min |
        Operator::F64Max |
        Operator::F64Copysign |
        Operator::I32TruncF32S |
        Operator::I32TruncF32U |
        Operator::I32TruncF64S |
        Operator::I32TruncF64U |
        Operator::I64TruncF32S |
        Operator::I64TruncF32U |
        Operator::I64TruncF64S |
        Operator::I64TruncF64U |
        Operator::F32ConvertI32S |
        Operator::F32ConvertI32U |
        Operator::F32ConvertI64S |
        Operator::F32ConvertI64U |
        Operator::F64ConvertI32S |
        Operator::F64ConvertI32U |
        Operator::F64ConvertI64S |
        Operator::F64ConvertI64U |
        Operator::F32DemoteF64 |
        Operator::F64PromoteF32 |
        Operator::I32ReinterpretF32 |
        Operator::I64ReinterpretF64 |
        Operator::F32ReinterpretI32 |
        Operator::F64ReinterpretI64 |

        // Reference types (RefFunc is remapped +1)
        Operator::RefFunc { .. } |
        Operator::RefNull { .. } |
        Operator::RefIsNull
    )
}

/// Reject globals with funcref type (their init expressions reference function indices
/// that we can't remap without rewriting the global section).
fn validate_globals(wasm: &[u8]) -> Result<(), String> {
    for payload in Parser::new(0).parse_all(wasm) {
        if let Ok(Payload::GlobalSection(reader)) = payload {
            for global in reader {
                let global = global.map_err(|e| e.to_string())?;
                if matches!(global.ty.content_type, ValType::Ref(_)) {
                    return Err("forbidden: global with funcref type".to_string());
                }
            }
        }
    }
    Ok(())
}

/// Validate the instrumented output is valid WASM.
fn validate_output(wasm: &[u8]) -> Result<(), String> {
    let parser = Parser::new(0);
    for payload in parser.parse_all(wasm) {
        payload.map_err(|e| format!("output validation failed: {}", e))?;
    }
    Ok(())
}

// --- Type converters ---

fn conv_vt(v: ValType) -> wasm_encoder::ValType {
    match v {
        ValType::I32 => wasm_encoder::ValType::I32,
        ValType::I64 => wasm_encoder::ValType::I64,
        ValType::F32 => wasm_encoder::ValType::F32,
        ValType::F64 => wasm_encoder::ValType::F64,
        ValType::V128 => wasm_encoder::ValType::V128,
        ValType::Ref(r) => wasm_encoder::ValType::Ref(conv_ref(r)),
    }
}

fn conv_ref(r: wasmparser::RefType) -> wasm_encoder::RefType {
    if r.is_func_ref() { wasm_encoder::RefType::FUNCREF }
    else if r.is_extern_ref() { wasm_encoder::RefType::EXTERNREF }
    else { wasm_encoder::RefType::FUNCREF }
}

fn conv_table(t: wasmparser::TableType) -> wasm_encoder::TableType {
    wasm_encoder::TableType {
        element_type: conv_ref(t.element_type),
        table64: t.table64, minimum: t.initial as u64,
        maximum: t.maximum.map(|m| m as u64), shared: t.shared,
    }
}

fn conv_mem(m: wasmparser::MemoryType) -> wasm_encoder::MemoryType {
    wasm_encoder::MemoryType {
        memory64: m.memory64, shared: m.shared, minimum: m.initial,
        maximum: m.maximum, page_size_log2: m.page_size_log2,
    }
}

fn conv_global(g: wasmparser::GlobalType) -> wasm_encoder::GlobalType {
    wasm_encoder::GlobalType {
        val_type: conv_vt(g.content_type), mutable: g.mutable, shared: g.shared,
    }
}
