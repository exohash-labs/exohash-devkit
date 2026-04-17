#!/usr/bin/env node
/**
 * Visual diff tool — compares a reference image against a screenshot.
 *
 * Usage:
 *   node scripts/visual-diff.js <reference.png> <screenshot.png> [diff-output.png]
 *
 * Output:
 *   - Diff image: red pixels = mismatch
 *   - Mismatch %
 *   - Per-region breakdown (header, hero, cards, footer)
 */

const fs = require("fs");
const sharp = require("sharp");
const pixelmatch = require("pixelmatch").default;

async function loadRaw(path, w, h) {
  const buf = await sharp(path)
    .resize(w, h, { fit: "fill" })
    .ensureAlpha()
    .raw()
    .toBuffer();
  return new Uint8Array(buf);
}

async function main() {
  const [,, refPath, curPath, diffPath] = process.argv;
  if (!refPath || !curPath) {
    console.error("Usage: node scripts/visual-diff.js <reference> <current> [diff.png]");
    process.exit(1);
  }

  // Normalize both to same size
  const W = 1056, H = 992;

  console.log(`Loading reference: ${refPath}`);
  console.log(`Loading current:   ${curPath}`);
  console.log(`Canvas:            ${W}x${H}\n`);

  const ref = await loadRaw(refPath, W, H);
  const cur = await loadRaw(curPath, W, H);
  const diff = new Uint8Array(W * H * 4);

  // Full image diff
  const totalMismatch = pixelmatch(ref, cur, diff, W, H, {
    threshold: 0.15,
    includeAA: false,
    alpha: 0.3,
  });

  const totalPixels = W * H;
  const pct = ((totalMismatch / totalPixels) * 100).toFixed(2);
  console.log(`=== FULL IMAGE ===`);
  console.log(`Mismatched pixels: ${totalMismatch.toLocaleString()} / ${totalPixels.toLocaleString()} (${pct}%)`);
  console.log(`Match score:       ${(100 - parseFloat(pct)).toFixed(2)}%\n`);

  // Per-region breakdown
  const regions = [
    { name: "Header",     y1: 0,   y2: 70 },
    { name: "DevNet+Hero", y1: 70,  y2: 380 },
    { name: "Subtitle+CTA", y1: 380, y2: 560 },
    { name: "GAMES head", y1: 560, y2: 640 },
    { name: "Cards",      y1: 640, y2: 920 },
    { name: "Footer",     y1: 920, y2: 992 },
  ];

  function regionDiff(y1, y2) {
    let mismatch = 0;
    let total = 0;
    for (let y = y1; y < Math.min(y2, H); y++) {
      for (let x = 0; x < W; x++) {
        const idx = (y * W + x) * 4;
        total++;
        // diff image: red channel > 0 means mismatch
        if (diff[idx] > 100 && diff[idx + 1] < 100) {
          mismatch++;
        }
      }
    }
    return { mismatch, total, pct: ((mismatch / total) * 100).toFixed(1) };
  }

  console.log("=== PER REGION ===");
  console.log("Region".padEnd(18) + "Mismatch".padEnd(12) + "Score");
  console.log("-".repeat(42));
  for (const r of regions) {
    const d = regionDiff(r.y1, r.y2);
    const score = (100 - parseFloat(d.pct)).toFixed(1);
    const bar = score >= 95 ? "OK" : score >= 85 ? "CLOSE" : "FIX";
    console.log(`${r.name.padEnd(18)}${(d.pct + "%").padEnd(12)}${score}% ${bar}`);
  }

  // Save diff image
  const outPath = diffPath || "/tmp/visual-diff.png";
  await sharp(Buffer.from(diff), { raw: { width: W, height: H, channels: 4 } })
    .png()
    .toFile(outPath);
  console.log(`\nDiff image saved: ${outPath}`);
}

main().catch(e => { console.error(e); process.exit(1); });
