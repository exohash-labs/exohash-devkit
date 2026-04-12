package chainsim

import (
	"encoding/binary"
	"fmt"
)

// InjectGasMetering instruments a WASM binary with gas metering using a
// mutable i64 global ($gas_used) that accumulates instruction counts.
//
// At every function entry: gas_used += instruction_count.
// At every loop back-edge: gas_used += 1.
//
// No limit check inside WASM — the host reads the exported "gas_used"
// global before and after each call to compute the delta. The host
// decides whether to kill the calculator based on per-call gas.
//
// For infinite loops, the host uses context.WithTimeout as safety net.
func InjectGasMetering(wasm []byte) ([]byte, error) {
	if len(wasm) < 8 || string(wasm[:4]) != "\x00asm" {
		return nil, fmt.Errorf("not a valid WASM binary")
	}
	version := binary.LittleEndian.Uint32(wasm[4:8])
	if version != 1 {
		return nil, fmt.Errorf("unsupported WASM version %d", version)
	}

	sections, err := parseSections(wasm)
	if err != nil {
		return nil, err
	}

	// Find existing global count.
	numGlobals := uint32(0)
	for _, s := range sections {
		if s.id == 6 {
			numGlobals, _ = readLEB128u(s.data)
		}
	}
	gasGlobalIdx := numGlobals

	// Validate no funcref globals.
	for _, s := range sections {
		if s.id == 6 {
			if err := validateGlobals(s.data); err != nil {
				return nil, err
			}
		}
	}

	// Rebuild module.
	out := make([]byte, 0, len(wasm)+2048)
	out = append(out, wasm[:8]...)

	for _, sec := range sections {
		switch sec.id {
		case 6: // Global section: append gas_used global
			out = appendGlobalSection(out, sec.data)
		case 7: // Export section: append gas_used export
			out = appendExportWithGasGlobal(out, sec.data, gasGlobalIdx)
		case 10: // Code section: instrument
			data, err := instrumentCodeSection(sec.data, gasGlobalIdx)
			if err != nil {
				return nil, err
			}
			out = appendRawSection(out, sec.id, data)
		default:
			out = appendRawSection(out, sec.id, sec.data)
		}
	}

	return out, nil
}

// ReadGasUsed reads the gas_used global from a wazero module instance.
// Returns total accumulated gas since instantiation.
func ReadGasUsed(mod interface{ ExportedGlobal(string) interface{ Get() uint64 } }) uint64 {
	g := mod.ExportedGlobal("gas_used")
	if g == nil {
		return 0
	}
	return g.Get()
}

// --- Section parsing ---

type wasmSection struct {
	id   byte
	data []byte
}

func parseSections(wasm []byte) ([]wasmSection, error) {
	var sections []wasmSection
	pos := 8
	for pos < len(wasm) {
		id := wasm[pos]
		pos++
		size, n := readLEB128u(wasm[pos:])
		pos += n
		if pos+int(size) > len(wasm) {
			return nil, fmt.Errorf("section %d: size %d exceeds binary", id, size)
		}
		sections = append(sections, wasmSection{id: id, data: wasm[pos : pos+int(size)]})
		pos += int(size)
	}
	return sections, nil
}

// --- Section builders ---

func appendRawSection(out []byte, id byte, data []byte) []byte {
	out = append(out, id)
	out = appendLEB128u(out, uint32(len(data)))
	out = append(out, data...)
	return out
}

func appendGlobalSection(out []byte, origData []byte) []byte {
	count, n := readLEB128u(origData)
	var body []byte
	body = appendLEB128u(body, count+1)
	body = append(body, origData[n:]...)

	// Append: (mut i64) = 0
	body = append(body, 0x7e) // i64
	body = append(body, 0x01) // mutable
	body = append(body, 0x42) // i64.const
	body = appendSLEB128(body, 0)
	body = append(body, 0x0b) // end

	return appendRawSection(out, 6, body)
}

func appendExportWithGasGlobal(out []byte, origData []byte, gasGlobalIdx uint32) []byte {
	count, n := readLEB128u(origData)
	var body []byte
	body = appendLEB128u(body, count+1)
	body = append(body, origData[n:]...)

	name := []byte("gas_used")
	body = appendLEB128u(body, uint32(len(name)))
	body = append(body, name...)
	body = append(body, 0x03) // global export
	body = appendLEB128u(body, gasGlobalIdx)

	return appendRawSection(out, 7, body)
}

// --- Validation ---

func validateGlobals(data []byte) error {
	count, n := readLEB128u(data)
	pos := n
	for i := uint32(0); i < count; i++ {
		valtype := data[pos]
		pos += 2 // valtype + mutability
		pos = skipConstExpr(data, pos)
		if valtype == 0x70 || valtype == 0x6f {
			return fmt.Errorf("forbidden: global with ref type 0x%02x", valtype)
		}
	}
	return nil
}

// --- Code instrumentation ---

func instrumentCodeSection(data []byte, gasGlobalIdx uint32) ([]byte, error) {
	count, n := readLEB128u(data)
	pos := n
	var body []byte
	body = appendLEB128u(body, count)

	for i := uint32(0); i < count; i++ {
		bodySize, n := readLEB128u(data[pos:])
		pos += n
		funcEnd := pos + int(bodySize)
		funcData := data[pos:funcEnd]
		pos = funcEnd

		instrumented, err := instrumentFunction(funcData, gasGlobalIdx)
		if err != nil {
			return nil, fmt.Errorf("function %d: %w", i, err)
		}
		body = appendLEB128u(body, uint32(len(instrumented)))
		body = append(body, instrumented...)
	}
	return body, nil
}

func instrumentFunction(funcData []byte, gasGlobalIdx uint32) ([]byte, error) {
	pos := 0

	// Parse locals.
	numLocalDecls, n := readLEB128u(funcData[pos:])
	pos += n
	localsEnd := pos
	for i := uint32(0); i < numLocalDecls; i++ {
		_, n := readLEB128u(funcData[pos:])
		pos += n
		pos++
		localsEnd = pos
	}

	codeStart := pos
	opCount, err := countOps(funcData[codeStart:])
	if err != nil {
		return nil, err
	}

	var out []byte
	out = append(out, funcData[:localsEnd]...)

	// Function entry: charge gas (no limit check — host checks delta after call).
	out = appendGasCharge(out, gasGlobalIdx, int64(opCount))

	// Re-emit instructions.
	pos = codeStart
	for pos < len(funcData) {
		opcode := funcData[pos]
		pos++

		operands, allowed, err := opcodeInfo(opcode, funcData[pos:])
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, fmt.Errorf("disallowed opcode 0x%02x", opcode)
		}

		if opcode == 0x03 { // loop
			out = append(out, 0x03)
			out = append(out, funcData[pos]) // block type
			pos++
			out = appendGasCharge(out, gasGlobalIdx, 1)
		} else {
			out = append(out, opcode)
			out = append(out, funcData[pos:pos+operands]...)
			pos += operands
		}
	}

	return out, nil
}

// gas_used += cost
func appendGasCharge(out []byte, gasGlobalIdx uint32, cost int64) []byte {
	out = append(out, 0x23) // global.get
	out = appendLEB128u(out, gasGlobalIdx)
	out = append(out, 0x42) // i64.const
	out = appendSLEB128(out, cost)
	out = append(out, 0x7c) // i64.add
	out = append(out, 0x24) // global.set
	out = appendLEB128u(out, gasGlobalIdx)
	return out
}


func countOps(code []byte) (int, error) {
	pos := 0
	count := 0
	for pos < len(code) {
		opcode := code[pos]
		pos++
		operands, _, err := opcodeInfo(opcode, code[pos:])
		if err != nil {
			return 0, err
		}
		pos += operands
		count++
	}
	return count, nil
}

func opcodeInfo(opcode byte, rest []byte) (int, bool, error) {
	switch opcode {
	case 0x00: return 0, true, nil
	case 0x01: return 0, true, nil
	case 0x05: return 0, true, nil
	case 0x0b: return 0, true, nil
	case 0x0f: return 0, true, nil
	case 0x1a: return 0, true, nil
	case 0x1b: return 0, true, nil
	case 0x02: return 1, true, nil
	case 0x03: return 1, true, nil
	case 0x04: return 1, true, nil
	case 0x0c: _, n := readLEB128u(rest); return n, true, nil
	case 0x0d: _, n := readLEB128u(rest); return n, true, nil
	case 0x0e: return brTableSize(rest), true, nil
	case 0x10: _, n := readLEB128u(rest); return n, true, nil
	case 0x11:
		_, n1 := readLEB128u(rest); _, n2 := readLEB128u(rest[n1:]); return n1 + n2, true, nil
	case 0x12: _, n := readLEB128u(rest); return n, true, nil
	case 0x20, 0x21, 0x22: _, n := readLEB128u(rest); return n, true, nil
	case 0x23, 0x24: _, n := readLEB128u(rest); return n, true, nil
	case 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f,
	     0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37,
	     0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e:
		_, n1 := readLEB128u(rest); _, n2 := readLEB128u(rest[n1:]); return n1 + n2, true, nil
	case 0x3f: _, n := readLEB128u(rest); return n, true, nil
	case 0x40: _, n := readLEB128u(rest); return n, true, nil
	case 0x41: _, n := readSLEB128(rest); return n, true, nil
	case 0x42: _, n := readSLEB128(rest); return n, true, nil
	case 0x43: return 4, true, nil
	case 0x44: return 8, true, nil
	case 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f,
	     0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a,
	     0x5b, 0x5c, 0x5d, 0x5e, 0x5f, 0x60, 0x61, 0x62, 0x63, 0x64, 0x65,
	     0x66, 0x67, 0x68, 0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f, 0x70,
	     0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b,
	     0x7c, 0x7d, 0x7e, 0x7f, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86,
	     0x87, 0x88, 0x89, 0x8a,
	     0x8b, 0x8c, 0x8d, 0x8e, 0x8f, 0x90, 0x91, 0x92, 0x93, 0x94, 0x95,
	     0x96, 0x97, 0x98, 0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9e, 0x9f, 0xa0,
	     0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab,
	     0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
	     0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf, 0xc0, 0xc1,
	     0xc2, 0xc3, 0xc4:
		return 0, true, nil
	case 0xd0: return 1, true, nil
	case 0xd1: return 0, true, nil
	case 0xd2: _, n := readLEB128u(rest); return n, true, nil
	case 0xfc:
		if len(rest) == 0 { return 0, false, fmt.Errorf("truncated 0xFC") }
		subop, n := readLEB128u(rest)
		switch subop {
		case 10: return n + 2, true, nil
		case 11: return n + 1, true, nil
		default: return 0, false, fmt.Errorf("disallowed 0xFC sub-opcode %d", subop)
		}
	default:
		return 0, false, fmt.Errorf("disallowed opcode 0x%02x", opcode)
	}
}

func brTableSize(data []byte) int {
	count, n := readLEB128u(data)
	pos := n
	for i := uint32(0); i <= count; i++ {
		_, n := readLEB128u(data[pos:]); pos += n
	}
	return pos
}

func skipConstExpr(data []byte, pos int) int {
	for pos < len(data) {
		op := data[pos]; pos++
		if op == 0x0b { return pos }
		switch op {
		case 0x41: _, n := readSLEB128(data[pos:]); pos += n
		case 0x42: _, n := readSLEB128(data[pos:]); pos += n
		case 0x23: _, n := readLEB128u(data[pos:]); pos += n
		case 0xd2: _, n := readLEB128u(data[pos:]); pos += n
		}
	}
	return pos
}

func readLEB128u(data []byte) (uint32, int) {
	var result uint32; var shift uint
	for i := 0; i < len(data) && i < 5; i++ {
		b := data[i]; result |= uint32(b&0x7f) << shift
		if b&0x80 == 0 { return result, i + 1 }
		shift += 7
	}
	return result, 1
}

func readSLEB128(data []byte) (int64, int) {
	var result int64; var shift uint; var i int
	for i = 0; i < len(data) && i < 10; i++ {
		b := data[i]; result |= int64(b&0x7f) << shift; shift += 7
		if b&0x80 == 0 {
			if shift < 64 && b&0x40 != 0 { result |= -(1 << shift) }
			return result, i + 1
		}
	}
	return result, i
}

func appendLEB128u(out []byte, val uint32) []byte {
	for {
		b := byte(val & 0x7f); val >>= 7
		if val != 0 { b |= 0x80 }
		out = append(out, b)
		if val == 0 { break }
	}
	return out
}

func appendSLEB128(out []byte, val int64) []byte {
	for {
		b := byte(val & 0x7f); val >>= 7
		done := (val == 0 && b&0x40 == 0) || (val == -1 && b&0x40 != 0)
		if !done { b |= 0x80 }
		out = append(out, b)
		if done { break }
	}
	return out
}
