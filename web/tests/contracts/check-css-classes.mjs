#!/usr/bin/env node
// Contract C2 linter (AUDIT_WEB.md 2.3): class names emitted as HTML (static
// files or JS template literals) vs class selectors in web/styles.css.
// WARN ONLY - many markup classes are legitimate JS hooks with no style, and
// some CSS rules target runtime-only states. Always exits 0.

import { join } from 'node:path';
import { printSummary, read, REPO_ROOT, webHtmlFiles, webJsFiles } from './lib.mjs';

// class="..." / class='...' attributes (escaped quotes tolerated inside JS
// template literals).
const CLASS_ATTR_RE = /\bclass\s*=\s*(?:\\?"((?:[^"\\]|\\.)*?)\\?"|\\?'((?:[^'\\]|\\.)*?)\\?')/g;
// classList.add('a', 'b') / classList.toggle('c', cond)
const CLASSLIST_RE = /classList\.(?:add|toggle)\(([^)]*)\)/g;
// element.className = 'a b' (DOM-built markup, heavily used by period-nav,
// charts tooltips and the ql-* components)
const CLASSNAME_RE = /\.className\s*(?:\+?=)\s*(['"`])([^'"`]*)\1/g;
const STRING_ARG_RE = /['"`]([A-Za-z_][A-Za-z0-9_-]*)['"`]/g;
const TOKEN_RE = /^[A-Za-z_][A-Za-z0-9_-]*$/;

// --- classes emitted in markup ---------------------------------------------
const usedClasses = new Set();

for (const file of [...webHtmlFiles(), ...webJsFiles()]) {
  const content = read(file);
  for (const m of content.matchAll(CLASS_ATTR_RE)) {
    // Drop `' + expr + '` spans first: in attributes built by string
    // concatenation the bare quotes are JS delimiters and `expr` is a
    // variable name, not a class token.
    const value = (m[1] ?? m[2] ?? '').replace(/(?<!\\)'\s*\+[\s\S]*?\+\s*(?<!\\)'/g, ' ');
    for (const token of value.split(/\s+/)) {
      // Skip interpolations (`${...}`) and anything not a plain class token.
      if (TOKEN_RE.test(token)) usedClasses.add(token);
    }
  }
  for (const m of content.matchAll(CLASSLIST_RE)) {
    for (const s of m[1].matchAll(STRING_ARG_RE)) usedClasses.add(s[1]);
  }
  for (const m of content.matchAll(CLASSNAME_RE)) {
    for (const token of m[2].split(/\s+/)) {
      if (TOKEN_RE.test(token)) usedClasses.add(token);
    }
  }
}

// --- classes styled in CSS --------------------------------------------------
// Tiny state machine: a selector is the text accumulated since the last
// `{`, `}` or `;` up to the next `{`. At-rule preludes (@media ...) are
// skipped; declaration blocks never reach the `{` handler because `;`
// resets the accumulator.
const css = read(join(REPO_ROOT, 'web', 'styles.css')).replace(/\/\*[\s\S]*?\*\//g, '');
const styledClasses = new Set();
let chunk = '';
for (const ch of css) {
  if (ch === '{') {
    if (!chunk.trimStart().startsWith('@')) {
      for (const m of chunk.matchAll(/\.([A-Za-z_][A-Za-z0-9_-]*)/g)) {
        styledClasses.add(m[1]);
      }
    }
    chunk = '';
  } else if (ch === '}' || ch === ';') {
    chunk = '';
  } else {
    chunk += ch;
  }
}

// --- report -----------------------------------------------------------------
const unstyled = [...usedClasses].filter((c) => !styledClasses.has(c)).sort();
const unref = [...styledClasses].filter((c) => !usedClasses.has(c)).sort();

console.log('== C2: emitted classes vs styles.css ==');
console.log(`classes in markup: ${usedClasses.size}, class selectors in CSS: ${styledClasses.size}`);

console.log(`\nWARN: ${unstyled.length} class(es) used in markup with no CSS rule (unstyled or JS hooks):`);
for (const c of unstyled) console.log(`  ${c}`);

console.log(`\nWARN: ${unref.length} class(es) styled in CSS but never emitted (dead-rule candidates):`);
for (const c of unref) console.log(`  ${c}`);

printSummary(0, unstyled.length + unref.length);
process.exit(0);
