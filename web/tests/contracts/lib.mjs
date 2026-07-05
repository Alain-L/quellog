// Shared helpers for the Gate-3 contract linters (AUDIT_WEB.md section 3).
// Plain Node, no dependencies.

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Repo root, resolved from this file's location (web/tests/contracts -> repo).
export const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');

// Recursively list files under dir whose name matches the given predicate.
export function walk(dir, match) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      out.push(...walk(full, match));
    } else if (match(name)) {
      out.push(full);
    }
  }
  return out;
}

// All versioned web JS sources: web/app.js + web/js/**/*.js.
// Bundles, vendored libs and tests are deliberately out of scope.
export function webJsFiles() {
  return [
    join(REPO_ROOT, 'web', 'app.js'),
    ...walk(join(REPO_ROOT, 'web', 'js'), (n) => n.endsWith('.js')),
  ];
}

// The static HTML surfaces that also carry inline handlers / class attributes.
export function webHtmlFiles() {
  return [
    join(REPO_ROOT, 'web', 'index.html'),
    join(REPO_ROOT, 'web', 'report.tmpl'),
    join(REPO_ROOT, 'web', 'report_split.tmpl'),
  ];
}

export function read(file) {
  return readFileSync(file, 'utf8');
}

export function rel(file) {
  return relative(REPO_ROOT, file);
}

// Compute the 1-based line number of an offset in a string.
export function lineOf(content, offset) {
  let line = 1;
  for (let i = 0; i < offset && i < content.length; i++) {
    if (content[i] === '\n') line++;
  }
  return line;
}

// Machine-readable trailer parsed by run.mjs for the summary table.
export function printSummary(errors, warnings) {
  console.log(`##SUMMARY errors=${errors} warnings=${warnings}`);
}
