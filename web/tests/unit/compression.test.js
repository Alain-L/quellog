import { test } from 'node:test';
import assert from 'node:assert/strict';
import zlib from 'node:zlib';
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

// R-7 — an extension-less tar member whose content sniffs as a PostgreSQL log
// (postgresql, pg_log_20260320, a jsonlog) must be kept, mirroring the CLI's
// content detection; genuinely foreign content stays dropped.
test('extractTar keeps extension-less members that look like logs, drops junk', async () => {
  const tar = buildTar([
    ['postgresql', '2026-01-01 12:00:00 UTC LOG:  extensionless stderr\n'],
    ['pg_log_20260320', '2026-01-01 12:00:01 UTC LOG:  dated rotation\n'],
    ['events_jsonlog', '{"log_time":"2026-01-01 12:00:02","message":"jsonlog line"}\n'],
    ['notes', 'just some free text, definitely not a log\n'],
  ]);

  const out = await extractTar(tar.buffer);

  assert.ok(out.includes('extensionless stderr'), 'keeps an extension-less stderr member');
  assert.ok(out.includes('dated rotation'), 'keeps a dated extension-less rotation');
  assert.ok(out.includes('jsonlog line'), 'keeps an extension-less jsonlog member');
  assert.ok(!out.includes('free text'), 'drops extension-less free text');
});

// FIX #1 — tar extraction must keep ROTATED PostgreSQL logs (postgresql.log.1,
// .log.2.gz, postgresql-16-main.log.1, postgresql.log.2026-03-23-10), which the
// old isSupportedEntry matched only as exact endings so it silently dropped
// every rotated member and kept just the live `.log`. The CLI
// (parser/tar_parser.go isRotatedLogFile) parses them all.
//
// A binary-capable tar builder (the entry helpers above take strings only).
function tarHeaderBin(name, size) {
  const h = new Uint8Array(512);
  h.set(enc.encode(name), 0);
  h.set(enc.encode(size.toString(8).padStart(11, '0')), 124);
  h[156] = 0x30; // '0' = regular file
  h.set(enc.encode('ustar\0'), 257);
  return h;
}
function buildTarBin(entries) {
  // entries: [name, Uint8Array]
  const parts = [];
  for (const [name, bytes] of entries) {
    const padded = new Uint8Array(Math.ceil(bytes.length / 512) * 512);
    padded.set(bytes);
    const out = new Uint8Array(512 + padded.length);
    out.set(tarHeaderBin(name, bytes.length));
    out.set(padded, 512);
    parts.push(out);
  }
  const total = parts.reduce((a, p) => a + p.length, 0) + 1024; // two zero blocks
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.length; }
  return out;
}

test('extractTar keeps rotated PostgreSQL logs and warns on unsupported entries', async () => {
  const line = (marker) => enc.encode(`2025-01-15 10:00:00.100 CET [100] LOG:  duration: 5.2 ms  statement: SELECT '${marker}'\n`);
  const tar = buildTarBin([
    ['postgresql.log',               line('LIVE')],                                 // live file
    ['postgresql.log.1',             line('ROT1')],                                 // numeric rotation
    ['postgresql-16-main.log.1',     line('ROT2')],                                 // prefixed + numeric rotation
    ['postgresql.log.2026-03-23-10', line('ROT3')],                                 // date rotation
    ['postgresql.log.2.gz',          new Uint8Array(zlib.gzipSync(Buffer.from(line('ROT4'))))], // rotated + nested gzip
    ['._postgresql.log',             enc.encode('GARBAGE_APPLEDOUBLE_RESOURCE_FORK')], // macOS sidecar, still rejected
    ['README.txt',                   enc.encode('not a log file at all')],          // unsupported -> warn
  ]);

  // Capture console.warn so we can assert the silent-loss warning now fires.
  const warnings = [];
  const origWarn = console.warn;
  console.warn = (...args) => warnings.push(args.join(' '));
  let out;
  try {
    out = await extractTar(tar.buffer);
  } finally {
    console.warn = origWarn;
  }

  // Every rotated member's content survived (was previously dropped).
  for (const m of ['LIVE', 'ROT1', 'ROT2', 'ROT3', 'ROT4']) {
    assert.ok(out.includes(m), `rotated/live member ${m} is kept`);
  }
  // AppleDouble sidecar and the unsupported .txt are still rejected.
  assert.ok(!out.includes('GARBAGE_APPLEDOUBLE'), 'AppleDouble ._ sidecar still rejected');
  assert.ok(!out.includes('not a log file'), 'unsupported .txt still rejected');

  // The unsupported entry is no longer dropped silently.
  assert.ok(warnings.some(w => w.includes('README.txt')), 'warns about the skipped unsupported entry');
  // The AppleDouble sidecar is a deliberate junk filter, so it stays quiet.
  assert.ok(!warnings.some(w => w.includes('._postgresql.log')), 'AppleDouble skip does not warn (deliberate junk)');
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
