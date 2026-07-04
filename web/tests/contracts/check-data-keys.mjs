#!/usr/bin/env node
// Contract C4 linter (AUDIT_WEB.md 2.3): key paths read by the JS renderers
// vs the REAL web payload. Ground truth is generated fresh on every run:
// bin/quellog --html on a fixture, then the embedded COMPRESSED_DATA blob is
// extracted, base64-decoded, zstd-decompressed and JSON-parsed.
// WARN ONLY - drift suspects may be optional sections absent from the
// fixture. Always exits 0.

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { zstdDecompressSync } from 'node:zlib';
import { printSummary, read, REPO_ROOT, rel, webJsFiles } from './lib.mjs';

const BIN = join(REPO_ROOT, 'bin', 'quellog');
const FIXTURE = join(REPO_ROOT, 'test', 'testdata', 'comprehensive', 'stderr.log');
const TMP = join(REPO_ROOT, 'web', 'tests', 'contracts', 'tmp');
const MAX_DEPTH = 4;

// Renderer files that read the payload (per the audit).
const RENDERERS = ['app.js', 'js/charts.js', 'js/report-filter.js', 'js/period-nav.js']
  .map((f) => join(REPO_ROOT, 'web', f));

// Heuristic (documented): local variables named `data` are ALSO used for
// chartData entries, WASM/worker messages and modal payloads. Chains whose
// first segment is one of these known non-payload roots are ignored; they
// never appear at the top level of buildJSONData output.
const IGNORED_ROOTS = new Set([
  'type', 'data', 'error', 'all', 'warningsOnly', 'warnings', 'length', 'id',
  'xData', 'yData', 'valueFormatter', 'plan', 'sql', 'logStart', 'logEnd',
  '_parseTimeMs', 'distances', 'types', 'queries', 'color', 'height',
]);

// Array/string built-ins: when a chain's last segment is a method call
// (followed by `(`) it is stripped; a trailing `.length` is stripped too.
const TAIL_PROPS = new Set(['length']);

// --- 1. generate a fresh report (never reuse a stale one) ------------------
function cleanTmpHtml() {
  if (!existsSync(TMP)) return;
  for (const f of readdirSync(TMP)) {
    if (f.endsWith('.html')) rmSync(join(TMP, f));
  }
}

if (!existsSync(BIN)) {
  console.log('== C4: JS data reads vs web payload ==');
  console.log(`WARN: ${rel(BIN)} not found - run "go build -o bin/quellog ." first. SKIPPED.`);
  printSummary(0, 1);
  process.exit(0);
}

mkdirSync(TMP, { recursive: true });
cleanTmpHtml();

let payload;
try {
  const res = spawnSync(BIN, [FIXTURE, '--html'], { cwd: TMP, encoding: 'utf8' });
  if (res.status !== 0) {
    throw new Error(`quellog --html failed (exit ${res.status}): ${res.stderr}`);
  }
  const html = read(join(TMP, 'stderr.html'));
  const m = html.match(/COMPRESSED_DATA = "((?:[^"\\]|\\.)*)"/);
  if (!m) throw new Error('COMPRESSED_DATA not found in generated report');
  // The base64 blob is JS-string-escaped by the Go template (\/ and \uXXXX);
  // JSON.parse of the quoted capture undoes exactly that.
  const b64 = JSON.parse(`"${m[1]}"`);
  payload = JSON.parse(zstdDecompressSync(Buffer.from(b64, 'base64')).toString('utf8'));
} finally {
  cleanTmpHtml();
}

// --- 2. collect key paths present in the payload ---------------------------
// Arrays are transparent: descend into element 0 without adding a segment.
const payloadPaths = new Set();
function collect(node, prefix, depth) {
  if (Array.isArray(node)) {
    if (node.length > 0) collect(node[0], prefix, depth);
    return;
  }
  if (node === null || typeof node !== 'object') return;
  for (const key of Object.keys(node)) {
    const path = prefix ? `${prefix}.${key}` : key;
    payloadPaths.add(path);
    if (depth + 1 < MAX_DEPTH) collect(node[key], path, depth + 1);
  }
}
collect(payload, '', 0);

// --- 3. collect member chains read by the renderers ------------------------
// data.<a>.<b>... and analysisData.<a>... (optional chaining tolerated);
// both roots normalize to the payload root.
const CHAIN_RE = /\b(?:data|analysisData)(?:\?\.|\.)([A-Za-z_$][\w$]*(?:(?:\?\.|\.)[A-Za-z_$][\w$]*)*)(\()?/g;
const readPaths = new Map(); // path -> first site "file:line"

for (const file of RENDERERS) {
  const content = read(file);
  const lines = content.split('\n');
  for (let i = 0; i < lines.length; i++) {
    for (const m of lines[i].matchAll(CHAIN_RE)) {
      let segs = m[1].replace(/\?\./g, '.').split('.');
      if (m[2]) segs.pop(); // trailing method call: drop it
      while (segs.length && TAIL_PROPS.has(segs[segs.length - 1])) segs.pop();
      if (!segs.length || IGNORED_ROOTS.has(segs[0])) continue;
      const path = segs.slice(0, MAX_DEPTH).join('.');
      if (!readPaths.has(path)) readPaths.set(path, `${rel(file)}:${i + 1}`);
    }
  }
}

// --- 4. report --------------------------------------------------------------
const matched = [];
const missing = [];
for (const [path, site] of [...readPaths.entries()].sort()) {
  (payloadPaths.has(path) ? matched : missing).push({ path, site });
}

console.log('== C4: JS data reads vs web payload ==');
console.log(`payload key paths (depth<=${MAX_DEPTH}): ${payloadPaths.size}, distinct paths read by JS: ${readPaths.size}`);
console.log(`matched: ${matched.length}`);

if (missing.length) {
  console.log(`\nWARN: ${missing.length} path(s) read by JS but ABSENT from the payload (drift suspects):`);
  for (const { path, site } of missing) console.log(`  ${path}  (${site})`);
} else {
  console.log('no drift suspects');
}

printSummary(0, missing.length);
process.exit(0);
