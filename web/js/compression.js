// Compression and archive handling for quellog web app

// Gunzip using native DecompressionStream API
export async function gunzipBuffer(buffer) {
    const ds = new DecompressionStream('gzip');
    const writer = ds.writable.getWriter();
    writer.write(buffer);
    writer.close();
    const reader = ds.readable.getReader();
    const chunks = [];
    while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(value);
    }
    const result = new Uint8Array(chunks.reduce((a, c) => a + c.length, 0));
    let offset = 0;
    for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.length; }
    return result;
}

// Zstd decompression using fzstd library (loaded in standalone mode)
export function unzstd(buffer) {
    if (typeof fzstd !== 'undefined') return fzstd.decompress(new Uint8Array(buffer));
    throw new Error('Zstd decompression not available');
}

// Detect compression format from magic bytes
export function detectFormat(buffer) {
    const h = new Uint8Array(buffer.slice(0, 512));
    if (h[0] === 0x1f && h[1] === 0x8b) return 'gzip';
    if (h[0] === 0x28 && h[1] === 0xb5 && h[2] === 0x2f && h[3] === 0xfd) return 'zstd';
    // ZIP: PK\x03\x04
    if (h[0] === 0x50 && h[1] === 0x4b && h[2] === 0x03 && h[3] === 0x04) return 'zip';
    // Tar: check for 'ustar' at offset 257
    if (h[257] === 0x75 && h[258] === 0x73 && h[259] === 0x74 && h[260] === 0x61 && h[261] === 0x72) return 'tar';
    return 'plain';
}

// Decompress buffer based on format detection and filename
export async function decompress(buffer, name) {
    let format = detectFormat(buffer);
    // Also check extension as fallback
    const lname = name.toLowerCase();
    if (format === 'plain' && (lname.endsWith('.gz') || lname.endsWith('.gzip'))) format = 'gzip';
    if (format === 'plain' && (lname.endsWith('.zst') || lname.endsWith('.zstd'))) format = 'zstd';

    if (format === 'gzip') return await gunzipBuffer(buffer);
    if (format === 'zstd') return unzstd(buffer);
    return new Uint8Array(buffer);
}

// Inflate raw deflate data using native DecompressionStream
async function inflateRaw(buffer) {
    const ds = new DecompressionStream('deflate-raw');
    const writer = ds.writable.getWriter();
    writer.write(buffer);
    writer.close();
    const reader = ds.readable.getReader();
    const chunks = [];
    while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(value);
    }
    const result = new Uint8Array(chunks.reduce((a, c) => a + c.length, 0));
    let offset = 0;
    for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.length; }
    return result;
}

// Supported log file extensions for archive extraction
const SUPPORTED_EXTS = ['.log', '.csv', '.json', '.jsonl'];

// Base extensions that PostgreSQL rotates (e.g. postgresql.log.1). Mirrors the
// CLI's isRotatedLogFile (parser/tar_parser.go), which only rotates .log/.csv.
const ROTATED_BASE_EXTS = ['.log', '.csv'];

function isSupportedEntry(name) {
    const lower = name.toLowerCase();
    // Direct match: a supported base extension, optionally followed by a nested
    // compression extension.
    if (SUPPORTED_EXTS.some(ext =>
        lower.endsWith(ext) || lower.endsWith(ext + '.gz') ||
        lower.endsWith(ext + '.zst') || lower.endsWith(ext + '.zstd')
    )) return true;
    // Rotated PostgreSQL logs: a supported base extension immediately followed
    // by a rotation suffix ("." + a digit), e.g. postgresql.log.1,
    // postgresql.log.2.gz, postgresql.log.2026-03-23-10, postgresql-16-main.log.1.
    // Mirrors the CLI's isRotatedLogFile so the browser keeps the same rotated
    // history the CLI parses instead of silently dropping it.
    return ROTATED_BASE_EXTS.some(base => {
        const marker = base + '.';
        const idx = lower.lastIndexOf(marker);
        if (idx === -1) return false;
        const after = lower.slice(idx + marker.length);
        return after.length > 0 && after[0] >= '0' && after[0] <= '9';
    });
}

// Extract ZIP archive and concatenate supported log entries.
//
// Driven by the central directory (the table at the end of the archive), not by
// the local file headers. Stream-written zips (general-purpose bit 3 / data
// descriptor) leave the size and CRC fields in the local header at 0 and only
// record the true values afterwards, so walking local headers extracts nothing;
// the central directory always carries the correct sizes. This mirrors Go's
// archive/zip, used by the CLI. Supports stored (method 0) and deflate (8).
// ZIP64 is not handled (unrealistic for a browser-uploaded log archive).
export async function extractZip(buffer) {
    const data = new Uint8Array(buffer);
    const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    const td = new TextDecoder();

    // Locate the End of Central Directory record (PK\x05\x06), scanning back
    // from the end since its trailing comment can be up to 65535 bytes.
    let eocd = -1;
    const scanFrom = Math.max(0, data.length - (22 + 0xffff));
    for (let i = data.length - 22; i >= scanFrom; i--) {
        if (view.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
    }
    if (eocd < 0) {
        console.warn('[quellog] zip: no end-of-central-directory record found');
        return '';
    }

    const entryCount = view.getUint16(eocd + 10, true);
    let cd = view.getUint32(eocd + 16, true); // offset of the central directory

    const files = [];
    for (let e = 0; e < entryCount && cd + 46 <= data.length; e++) {
        if (view.getUint32(cd, true) !== 0x02014b50) break; // not a CD file header

        const method = view.getUint16(cd + 10, true);
        const compSize = view.getUint32(cd + 20, true); // authoritative, unlike the local header
        const nameLen = view.getUint16(cd + 28, true);
        const extraLen = view.getUint16(cd + 30, true);
        const commentLen = view.getUint16(cd + 32, true);
        const localOff = view.getUint32(cd + 42, true);
        const name = td.decode(data.subarray(cd + 46, cd + 46 + nameLen));
        cd += 46 + nameLen + extraLen + commentLen;

        if (name.endsWith('/')) continue; // directory
        const baseName = name.includes('/') ? name.substring(name.lastIndexOf('/') + 1) : name;
        // Keep only supported log entries; skip macOS AppleDouble sidecars
        // (._foo, which end in .log yet hold binary data) and path traversal.
        if (!isSupportedEntry(baseName) || baseName.startsWith('._') || name.includes('..')) continue;

        // The local header's name/extra lengths can differ from the central
        // directory's, so read them from the local header to find the data.
        if (localOff + 30 > data.length || view.getUint32(localOff, true) !== 0x04034b50) {
            console.warn(`[quellog] zip: bad local header for ${name}`);
            continue;
        }
        const lNameLen = view.getUint16(localOff + 26, true);
        const lExtraLen = view.getUint16(localOff + 28, true);
        const dataStart = localOff + 30 + lNameLen + lExtraLen;
        if (dataStart + compSize > data.length) {
            console.warn(`[quellog] zip: truncated entry ${name}`);
            continue;
        }

        let content;
        if (method === 0) {
            content = data.slice(dataStart, dataStart + compSize); // stored
        } else if (method === 8) {
            content = await inflateRaw(data.slice(dataStart, dataStart + compSize)); // deflate
        } else {
            console.warn(`[quellog] zip: skipping ${name}, unsupported method ${method}`);
            continue;
        }

        // Handle nested compression (.gz, .zst)
        const lname = baseName.toLowerCase();
        if (lname.endsWith('.gz') || lname.endsWith('.gzip')) {
            content = await gunzipBuffer(content.buffer);
        } else if (lname.endsWith('.zst') || lname.endsWith('.zstd')) {
            content = unzstd(content.buffer);
        }

        files.push({ name: baseName, content });
    }

    return files.map(f => td.decode(f.content)).join('\n');
}

// Extract tar archive and concatenate file contents
export async function extractTar(buffer) {
    const data = new Uint8Array(buffer);
    const files = [];
    let offset = 0;

    while (offset + 512 <= data.length) {
        const header = data.slice(offset, offset + 512);
        // End of archive: all zeros
        if (header.every(b => b === 0)) break;

        const nameBytes = header.slice(0, 100);
        const name = new TextDecoder().decode(nameBytes).replace(/\0.*$/, '');
        const sizeStr = new TextDecoder().decode(header.slice(124, 136)).replace(/\0.*$/, '').trim();
        const size = parseInt(sizeStr, 8) || 0;
        const typeFlag = header[156];

        offset += 512;

        // Regular file (type '0' or '\0')
        if ((typeFlag === 48 || typeFlag === 0) && size > 0) {
            const baseName = name.includes('/') ? name.substring(name.lastIndexOf('/') + 1) : name;
            // Skip macOS AppleDouble sidecars (._foo, which end in .log yet hold
            // binary resource-fork data) and path-traversal names; concatenating
            // them would poison format detection and the log content. These are
            // deliberate junk filters, not "unsupported" logs, so they stay quiet.
            if (baseName.startsWith('._') || name.includes('..')) {
                // dropped on purpose — no warning
            } else if (isSupportedEntry(baseName)) {
                let content = data.slice(offset, offset + size);
                // Decompress nested files
                const lname = baseName.toLowerCase();
                if (lname.endsWith('.gz') || lname.endsWith('.gzip')) {
                    content = await gunzipBuffer(content.buffer);
                } else if (lname.endsWith('.zst') || lname.endsWith('.zstd')) {
                    content = unzstd(content.buffer);
                }
                files.push({ name: baseName, content });
            } else {
                // Warn instead of silently dropping: an unrecognized name may be
                // a mislabeled or unexpectedly-rotated log the user meant to
                // include (rotated .log/.csv are now kept by isSupportedEntry).
                console.warn(`[quellog] tar: skipping unsupported entry ${name}`);
            }
        }

        offset += Math.ceil(size / 512) * 512;
    }

    // Concatenate all file contents
    return files.map(f => new TextDecoder().decode(f.content)).join('\n');
}

// Prepare file content: decompress and extract if needed.
// Returns Uint8Array for plain (non-archive) inputs so the wasm
// pipeline can use quellogParseBytes (avoids string conversion +
// []byte copy that doubled wasm linear memory pressure on big logs).
// Archive paths (zip/tar) still return strings for legacy reasons —
// they tend to be smaller anyway.
export async function prepareContent(file) {
    const buffer = await file.arrayBuffer();
    const lname = file.name.toLowerCase();

    // ZIP archives: extract directly (no outer decompression needed)
    const initialFormat = detectFormat(buffer);
    if (initialFormat === 'zip' || lname.endsWith('.zip')) {
        return await extractZip(buffer);
    }

    let data = await decompress(buffer, file.name);

    // Check if result is a tar archive
    const format = detectFormat(data.buffer);
    if (format === 'tar' || lname.includes('.tar')) {
        return await extractTar(data.buffer);
    }

    // Return Uint8Array directly — caller decides whether to decode.
    return data;
}
