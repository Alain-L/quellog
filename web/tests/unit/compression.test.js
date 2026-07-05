import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractTar } from '../../js/compression.js';

const enc = new TextEncoder();

// Minimal ustar header (extractTar reads name[0..100], size[124..136] octal,
// typeflag[156]); checksum/magic are not validated by the extractor.
function tarHeader(name, size) {
  const h = new Uint8Array(512);
  h.set(enc.encode(name), 0);
  h.set(enc.encode(size.toString(8).padStart(11, '0')), 124);
  h[156] = 0x30; // '0' = regular file
  h.set(enc.encode('ustar\0'), 257);
  return h;
}

function tarEntry(name, content) {
  const bytes = enc.encode(content);
  const padded = new Uint8Array(Math.ceil(bytes.length / 512) * 512);
  padded.set(bytes);
  const out = new Uint8Array(512 + padded.length);
  out.set(tarHeader(name, bytes.length));
  out.set(padded, 512);
  return out;
}

function buildTar(entries) {
  const parts = entries.map(([n, c]) => tarEntry(n, c));
  const total = parts.reduce((a, p) => a + p.length, 0) + 1024; // two zero blocks
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.length; }
  return out;
}

test('extractTar keeps supported logs, skips AppleDouble and unsupported entries', async () => {
  const tar = buildTar([
    ['._app.log', 'GARBAGE_APPLEDOUBLE_RESOURCE_FORK'], // macOS sidecar, ends in .log
    ['app.log', '2026-01-01 12:00:00 UTC LOG:  hello world\n'],
    ['README.txt', 'not a log file at all'],
    ['logs/db.log', '2026-01-01 12:00:01 UTC LOG:  nested path\n'],
  ]);

  const out = await extractTar(tar.buffer);

  assert.ok(out.includes('hello world'), 'keeps a real .log');
  assert.ok(out.includes('nested path'), 'keeps a .log under a subdirectory (by basename)');
  assert.ok(!out.includes('GARBAGE_APPLEDOUBLE'), 'drops the ._ AppleDouble sidecar');
  assert.ok(!out.includes('not a log file'), 'drops an unsupported .txt entry');
});
