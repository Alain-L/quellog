// Pixel comparison: pixelmatch over pngjs buffers, with padding so that
// dimension mismatches still produce a reviewable diff PNG.

import { readFileSync, writeFileSync } from 'node:fs';
import { PNG } from 'pngjs';
import pixelmatch from 'pixelmatch';

/** Pad a PNG to width x height (top-left anchored, magenta filler). */
function padTo(png, width, height) {
  if (png.width === width && png.height === height) return png;
  const out = new PNG({ width, height });
  // Magenta filler makes padded (i.e. size-mismatch) areas obvious in diffs.
  for (let i = 0; i < out.data.length; i += 4) {
    out.data[i] = 255; out.data[i + 1] = 0; out.data[i + 2] = 255; out.data[i + 3] = 255;
  }
  PNG.bitblt(png, out, 0, 0, png.width, png.height, 0, 0);
  return out;
}

/**
 * Compare two PNG files. Writes a diff PNG to diffPath on mismatch.
 * @returns {{pass: boolean, reason?: string, diffPixels: number,
 *            totalPixels: number, ratio: number}}
 */
export function comparePng(baselinePath, actualPath, diffPath, { threshold = 0.1, maxDiffRatio = 0.005 } = {}) {
  const base = PNG.sync.read(readFileSync(baselinePath));
  const act = PNG.sync.read(readFileSync(actualPath));

  const dimsMatch = base.width === act.width && base.height === act.height;
  const width = Math.max(base.width, act.width);
  const height = Math.max(base.height, act.height);
  const a = padTo(base, width, height);
  const b = padTo(act, width, height);

  const diff = new PNG({ width, height });
  const diffPixels = pixelmatch(a.data, b.data, diff.data, width, height, { threshold });
  const totalPixels = width * height;
  const ratio = diffPixels / totalPixels;

  // A dimension change is a layout change: always a failure.
  const pass = dimsMatch && ratio <= maxDiffRatio;
  if (!pass) {
    writeFileSync(diffPath, PNG.sync.write(diff));
  }
  return {
    pass,
    reason: dimsMatch
      ? (pass ? undefined : `${(ratio * 100).toFixed(3)}% of pixels differ (limit ${(maxDiffRatio * 100).toFixed(1)}%)`)
      : `dimensions differ: baseline ${base.width}x${base.height} vs actual ${act.width}x${act.height}`,
    diffPixels,
    totalPixels,
    ratio,
  };
}
