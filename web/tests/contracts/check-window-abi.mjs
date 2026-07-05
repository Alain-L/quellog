#!/usr/bin/env node
// Contract C1 linter (AUDIT_WEB.md 2.3): every identifier invoked from an
// inline handler attribute (onclick=... in index.html, the templates, or in
// HTML emitted from JS template literals) MUST be assigned on window.
// A missing assignment is a silently dead button -> FAIL HARD (exit 1).
//
// Also reports (informational, never fails) window.* assignments that no
// inline handler and no other code references -> candidates for removal.
//
// Pragmatic regex pass over raw file contents; no HTML/JS parser.

import { lineOf, printSummary, read, rel, webHtmlFiles, webJsFiles } from './lib.mjs';

// Legitimate exceptions: handler identifiers allowed to NOT be on window.
const ALLOWLIST = [];

// JS keywords and browser globals that can appear as `name(` inside a handler
// body but are not part of the window ABI this linter guards.
const BUILTINS = new Set([
  // keywords / syntax
  'if', 'else', 'for', 'while', 'do', 'switch', 'catch', 'return', 'function',
  'typeof', 'new', 'delete', 'void', 'in', 'of', 'this',
  // ambient browser globals
  'event', 'window', 'document', 'alert', 'confirm', 'prompt', 'console',
  'parseInt', 'parseFloat', 'isNaN', 'String', 'Number', 'Boolean', 'Array',
  'Object', 'Date', 'Math', 'JSON', 'encodeURIComponent', 'decodeURIComponent',
  'setTimeout', 'setInterval', 'requestAnimationFrame',
]);

const HANDLER_EVENTS = 'click|change|input|keyup|mouseover|mouseout';
// Attribute with a double- or single-quoted body (escaped quotes allowed,
// as found inside JS template literals).
const HANDLER_RE = new RegExp(
  `\\bon(?:${HANDLER_EVENTS})\\s*=\\s*(?:"((?:[^"\\\\]|\\\\.)*)"|'((?:[^'\\\\]|\\\\.)*)')`,
  'g'
);
// Identifier directly followed by an open paren = an invocation. Member
// calls (.foo() / ?.foo()) are excluded by the look-behind on `.`.
const CALL_RE = /(^|[^.\w$])([A-Za-z_$][A-Za-z0-9_$]*)\s*\(/g;
// window.NAME = (but not == / === / =>).
const ASSIGN_RE = /window\.([A-Za-z_$][A-Za-z0-9_$]*)\s*=(?![=>])/g;

// Remove `${...}` interpolation spans (balanced braces) from a handler body
// found inside a JS template literal: code in an interpolation runs at
// template-build time in module scope, not in the browser event handler,
// so its identifiers are not part of the window ABI.
function stripInterpolations(body) {
  let out = '';
  let i = 0;
  while (i < body.length) {
    if (body[i] === '$' && body[i + 1] === '{') {
      let depth = 1;
      i += 2;
      while (i < body.length && depth > 0) {
        if (body[i] === '{') depth++;
        else if (body[i] === '}') depth--;
        i++;
      }
      // Unbalanced (attribute capture truncated mid-interpolation): drop rest.
      continue;
    }
    out += body[i++];
  }
  return out;
}

// Same idea for attributes built by string concatenation:
// onclick="f(\'' + expr + '\')" - the bare quotes are JS string delimiters,
// so `' + expr + '` is build-time code. Escaped quotes (\') stay: they are
// part of the handler. Drop every bare-quote + ... + bare-quote span.
function stripConcatenations(body) {
  return body.replace(/(?<!\\)'\s*\+[\s\S]*?\+\s*(?<!\\)'/g, '');
}

const files = [...webHtmlFiles(), ...webJsFiles()];

// --- collect handler invocations and window assignments -------------------
const handlerCalls = new Map(); // name -> [{file, line}]
const assignments = new Map(); // name -> [{file, line}]
const contents = new Map(); // file -> content

for (const file of files) {
  const content = read(file);
  contents.set(file, content);

  for (const m of content.matchAll(HANDLER_RE)) {
    const body = stripConcatenations(stripInterpolations(m[1] ?? m[2] ?? ''));
    for (const c of body.matchAll(CALL_RE)) {
      const name = c[2];
      if (BUILTINS.has(name)) continue;
      if (!handlerCalls.has(name)) handlerCalls.set(name, []);
      handlerCalls.get(name).push({ file, line: lineOf(content, m.index) });
    }
  }

  for (const m of content.matchAll(ASSIGN_RE)) {
    const name = m[1];
    if (!assignments.has(name)) assignments.set(name, []);
    assignments.get(name).push({ file, line: lineOf(content, m.index) });
  }
}

// --- 1. handler identifiers with no window assignment -> HARD FAIL --------
const violations = [...handlerCalls.keys()]
  .filter((name) => !assignments.has(name) && !ALLOWLIST.includes(name))
  .sort();

console.log('== C1: window ABI vs inline handlers ==');
console.log(`handler identifiers: ${handlerCalls.size}, window assignments: ${assignments.size}`);

if (violations.length) {
  console.log(`\nERROR: ${violations.length} handler identifier(s) never assigned on window (dead buttons):`);
  for (const name of violations) {
    for (const site of handlerCalls.get(name)) {
      console.log(`  ${name}  <- ${rel(site.file)}:${site.line}`);
    }
  }
} else {
  console.log('all handler identifiers are assigned on window: OK');
}

// --- 2. window assignments never referenced anywhere -> WARN ONLY ---------
// A name counts as referenced if an inline handler invokes it, or any scanned
// file reads window.NAME (non-assignment position) or calls bare NAME(.
const unused = [];
for (const name of [...assignments.keys()].sort()) {
  if (handlerCalls.has(name)) continue;
  const windowReadRe = new RegExp(`window\\.${name}\\b(?!\\s*=(?![=>]))`);
  const bareCallRe = new RegExp(`(?<!\\.|function |function)\\b${name}\\s*\\(`);
  let referenced = false;
  for (const content of contents.values()) {
    if (windowReadRe.test(content) || bareCallRe.test(content)) {
      referenced = true;
      break;
    }
  }
  if (!referenced) unused.push(name);
}

if (unused.length) {
  console.log(`\nWARN: ${unused.length} window.* assignment(s) with no handler or code reference (removal candidates):`);
  for (const name of unused) {
    const site = assignments.get(name)[0];
    console.log(`  window.${name}  (${rel(site.file)}:${site.line})`);
  }
}

printSummary(violations.length, unused.length);
process.exit(violations.length ? 1 : 0);
