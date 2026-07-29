#!/usr/bin/env node
// Gate-3 orchestrator (AUDIT_WEB.md section 3): runs the three contract
// linters in order and prints a compact summary table.
// Exit code is non-zero iff check-window-abi (C1, fail-hard) fails.

import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

const LINTERS = [
  { name: 'window-abi (C1)', file: 'check-window-abi.mjs', hard: true },
  { name: 'css-classes (C2)', file: 'check-css-classes.mjs', hard: false },
  { name: 'data-keys (C4)', file: 'check-data-keys.mjs', hard: false },
];

const rows = [];
let failed = false;

for (const linter of LINTERS) {
  const res = spawnSync(process.execPath, [join(HERE, linter.file)], { encoding: 'utf8' });
  process.stdout.write(res.stdout.replace(/^##SUMMARY.*\n?/m, ''));
  if (res.stderr) process.stderr.write(res.stderr);
  console.log('');

  const m = res.stdout.match(/^##SUMMARY errors=(\d+) warnings=(\d+)/m);
  const errors = m ? Number(m[1]) : NaN;
  const warnings = m ? Number(m[2]) : NaN;
  const crashed = res.status !== 0 && (!linter.hard || !m);
  rows.push({
    name: linter.name,
    errors: Number.isNaN(errors) ? '?' : errors,
    warnings: Number.isNaN(warnings) ? '?' : warnings,
    status: crashed ? 'CRASH' : res.status !== 0 ? 'FAIL' : 'ok',
  });
  if (res.status !== 0 && linter.hard) failed = true;
  if (crashed) failed = true;
}

console.log('== Gate 3 summary ==');
console.log('linter             errors  warnings  status');
for (const r of rows) {
  console.log(`${r.name.padEnd(19)}${String(r.errors).padStart(6)}${String(r.warnings).padStart(10)}  ${r.status}`);
}

process.exit(failed ? 1 : 0);
