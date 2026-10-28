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
// pgBackRest logs added to the fixture so that the payload carries a
// "pgbackrest" key: S2 (async worker, a dropped WAL segment, failure
// stretches, PostgreSQL's archive_command failures), S3 (a manual expire
// --set) and a log-timestamp=n file (missing_info). The --begin bound
// before any line adds the "window" key without filtering anything out.
// No fixture has an archive-get failure: archive_get.failures stays a
// known drift suspect.
const PGBR_DIR = join(REPO_ROOT, 'test', 'testdata', 'pgbackrest');
const PGBR_INPUTS = [
  join(PGBR_DIR, 'S2', 'pgbackrest_logs'),
  join(PGBR_DIR, 'S2', 'pg_logs_main', 'postgresql.log'),
  join(PGBR_DIR, 'S3', 'pgbackrest_logs'),
  join(PGBR_DIR, 'S4', '02_timestamp', 'check_console_no_timestamp.out'),
];
const PGBR_ARGS = ['--begin', '2000-01-01 00:00:00'];
const TMP = join(REPO_ROOT, 'web', 'tests', 'contracts', 'tmp');
const MAX_DEPTH = 4;

// Renderer files that read the payload (per the audit). app.js was split
// into per-section modules (AUDIT_WEB.md phase 4); list them explicitly
// alongside the files that still read the payload directly.
const RENDERERS = [
  'app.js', 'js/charts.js', 'js/report-filter.js', 'js/period-nav.js',
  'js/sections/summary.js', 'js/sections/events.js', 'js/sections/connections.js',
  'js/sections/checkpoints.js', 'js/sections/maintenance.js', 'js/sections/locks.js',
  'js/sections/tempfiles.js', 'js/sections/sql.js', 'js/sections/modals.js',
  'js/sections/patroni.js',
  'js/sections/pgbackrest.js',
].map((f) => join(REPO_ROOT, 'web', f));

// Heuristic (documented): local variables named `data` are ALSO used for
// chartData entries, WASM/worker messages and modal payloads. Chains whose
// first segment is one of these known non-payload roots are ignored; they
// never appear at the top level of buildJSONData output.
const IGNORED_ROOTS = new Set([
  'type', 'data', 'error', 'all', 'warningsOnly', 'warnings', 'length', 'id',
  'xData', 'yData', 'valueFormatter', 'plan', 'sql', 'logStart', 'logEnd',
  '_parseTimeMs', 'distances', 'types', 'queries', 'color', 'height',
  // Client-side-only markers written by js/report-filter.js on the filtered
  // payload (never in the backend JSON) — read by the section renderers to
  // annotate whole-log sections under a time filter.
  '_wholeLog', '_timeFiltered', '_filterRange',
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
  const res = spawnSync(BIN, [FIXTURE, ...PGBR_INPUTS, ...PGBR_ARGS, '--html'], { cwd: TMP, encoding: 'utf8' });
  if (res.status !== 0) {
    throw new Error(`quellog --html failed (exit ${res.status}): ${res.stderr}`);
  }
  const report = readdirSync(TMP).find((f) => f.endsWith('.html'));
  if (!report) throw new Error('quellog --html wrote no report');
  const html = read(join(TMP, report));
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
// Arrays are transparent: descend into every element without adding a
// segment, so a key only some elements carry (one pgBackRest stanza out of
// several) still counts as present.
const payloadPaths = new Set();
function collect(node, prefix, depth) {
  if (Array.isArray(node)) {
    for (const el of node) collect(el, prefix, depth);
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

// Local names that stand for a payload sub-object in one renderer: the
// pgBackRest section reads each stanza as `st`, its archive stats as
// `push`/`get`, so their member chains are checked under those paths.
const ALIASES = {
  'js/sections/pgbackrest.js': {
    st: 'pgbackrest',
    push: 'pgbackrest.archive_push',
    get: 'pgbackrest.archive_get',
  },
};

for (const file of RENDERERS) {
  const content = read(file);
  const lines = content.split('\n');
  const aliases = ALIASES[file.slice(join(REPO_ROOT, 'web').length + 1)] || {};
  const aliasRe = Object.keys(aliases).length
    ? new RegExp(`\\b(${Object.keys(aliases).join('|')})(?:\\?\\.|\\.)([A-Za-z_$][\\w$]*(?:(?:\\?\\.|\\.)[A-Za-z_$][\\w$]*)*)(\\()?`, 'g')
    : null;
  for (let i = 0; i < lines.length; i++) {
    if (aliasRe) {
      for (const m of lines[i].matchAll(aliasRe)) {
        let segs = m[2].replace(/\?\./g, '.').split('.');
        if (m[3]) segs.pop();
        while (segs.length && TAIL_PROPS.has(segs[segs.length - 1])) segs.pop();
        if (!segs.length) continue;
        const path = [...aliases[m[1]].split('.'), ...segs].slice(0, MAX_DEPTH).join('.');
        if (!readPaths.has(path)) readPaths.set(path, `${rel(file)}:${i + 1}`);
      }
    }
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
