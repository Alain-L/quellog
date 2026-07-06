import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractTar, extractZip } from '../../js/compression.js';

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

const u16 = (n) => [n & 0xff, (n >> 8) & 0xff];
const u32 = (n) => [n & 0xff, (n >> 8) & 0xff, (n >> 16) & 0xff, (n >>> 24) & 0xff];

// Build a stored (method 0) zip. When streamed=true, local headers set the
// data-descriptor flag (bit 3) and leave size/crc at 0, with the true values
// only in the central directory - exactly how stream-written zips look.
function buildZip(entries, streamed) {
  const parts = [];
  let offset = 0;
  const push = (bytes) => {
    const a = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    parts.push(a);
    offset += a.length;
  };
  const central = [];
  for (const { name, content } of entries) {
    const nameB = enc.encode(name);
    const dataB = enc.encode(content);
    const localOff = offset;
    const flag = streamed ? 0x08 : 0x00;
    const sz = streamed ? 0 : dataB.length; // local header sizes zeroed when streamed
    push([...u32(0x04034b50), ...u16(20), ...u16(flag), ...u16(0), ...u16(0), ...u16(0),
      ...u32(0), ...u32(sz), ...u32(sz), ...u16(nameB.length), ...u16(0)]);
    push(nameB);
    push(dataB);
    if (streamed) {
      // data descriptor: signature + crc + compSize + uncompSize
      push([...u32(0x08074b50), ...u32(0), ...u32(dataB.length), ...u32(dataB.length)]);
    }
    central.push({ name: nameB, localOff, size: dataB.length });
  }
  const cdStart = offset;
  for (const c of central) {
    push([...u32(0x02014b50), ...u16(20), ...u16(20), ...u16(streamed ? 0x08 : 0), ...u16(0),
      ...u16(0), ...u16(0), ...u32(0), ...u32(c.size), ...u32(c.size),
      ...u16(c.name.length), ...u16(0), ...u16(0), ...u16(0), ...u16(0), ...u32(0), ...u32(c.localOff)]);
    push(c.name);
  }
  const cdSize = offset - cdStart;
  push([...u32(0x06054b50), ...u16(0), ...u16(0), ...u16(central.length), ...u16(central.length),
    ...u32(cdSize), ...u32(cdStart), ...u16(0)]);
  const out = new Uint8Array(offset);
  let o = 0;
  for (const p of parts) { out.set(p, o); o += p.length; }
  return out;
}

test('extractZip reads stream-written zips (data descriptor, zeroed local sizes)', async () => {
  // The regression: local headers say size 0, so header-walking extracted nothing.
  const zip = buildZip([{ name: 'app.log', content: '2026-01-01 12:00:00 UTC LOG:  hello from a streamed zip\n' }], true);
  const out = await extractZip(zip.buffer);
  assert.ok(out.includes('hello from a streamed zip'), 'extracts the log via the central directory');
});

test('extractZip still reads normal zips and filters junk', async () => {
  const zip = buildZip([
    { name: 'db.log', content: '2026-01-01 12:00:00 UTC LOG:  normal zip entry\n' },
    { name: '._db.log', content: 'GARBAGE_APPLEDOUBLE_IN_ZIP' },
    { name: 'notes.txt', content: 'not a log file' },
  ], false);
  const out = await extractZip(zip.buffer);
  assert.ok(out.includes('normal zip entry'), 'keeps the .log');
  assert.ok(!out.includes('GARBAGE_APPLEDOUBLE'), 'skips the ._ AppleDouble sidecar');
  assert.ok(!out.includes('not a log file'), 'skips the unsupported .txt');
});
