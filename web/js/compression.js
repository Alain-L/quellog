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

function isSupportedEntry(name) {
    const lower = name.toLowerCase();
    return SUPPORTED_EXTS.some(ext =>
        lower.endsWith(ext) || lower.endsWith(ext + '.gz') ||
        lower.endsWith(ext + '.zst') || lower.endsWith(ext + '.zstd')
    );
}

// Extract ZIP archive and concatenate file contents
// Parses local file headers; supports stored (method 0) and deflate (method 8)
export async function extractZip(buffer) {
    const data = new Uint8Array(buffer);
    const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    const files = [];
    let offset = 0;

    while (offset + 30 <= data.length) {
        const sig = view.getUint32(offset, true);
        if (sig !== 0x04034b50) break; // Not a local file header

        const method = view.getUint16(offset + 8, true);
        const compSize = view.getUint32(offset + 18, true);
        const nameLen = view.getUint16(offset + 26, true);
        const extraLen = view.getUint16(offset + 28, true);

        // Validate that name and extra fields fit within the buffer
        if (offset + 30 + nameLen + extraLen > data.length) {
            console.warn('[quellog] Truncated zip entry header');
            break;
        }

        const name = new TextDecoder().decode(data.subarray(offset + 30, offset + 30 + nameLen));

        const dataStart = offset + 30 + nameLen + extraLen;

        // Validate that compressed data fits within the buffer
        if (dataStart + compSize > data.length) {
            console.warn(`[quellog] Truncated zip entry: ${name}`);
            break;
        }

        offset = dataStart + compSize;

        // Skip directories and unsupported files
        if (name.endsWith('/') || compSize === 0) continue;

        const baseName = name.includes('/') ? name.substring(name.lastIndexOf('/') + 1) : name;
        if (!isSupportedEntry(baseName)) continue;

        // Path traversal protection
        if (name.includes('..')) continue;

        let content;
        if (method === 0) {
            // Stored (no compression)
            content = data.slice(dataStart, dataStart + compSize);
        } else if (method === 8) {
            // Deflate
            content = await inflateRaw(data.slice(dataStart, dataStart + compSize));
        } else {
            console.warn(`[quellog] Skipping ${name}: unsupported compression method ${method}`);
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

    return files.map(f => new TextDecoder().decode(f.content)).join('\n');
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
            let content = data.slice(offset, offset + size);
            // Decompress nested files
            const lname = name.toLowerCase();
            if (lname.endsWith('.gz') || lname.endsWith('.gzip')) {
                content = await gunzipBuffer(content.buffer);
            } else if (lname.endsWith('.zst') || lname.endsWith('.zstd')) {
                content = unzstd(content.buffer);
            }
            files.push({ name, content });
        }

        offset += Math.ceil(size / 512) * 512;
    }

    // Concatenate all file contents
    return files.map(f => new TextDecoder().decode(f.content)).join('\n');
}

// Prepare file content: decompress and extract if needed
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

    return new TextDecoder().decode(data);
}
