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
    let data = await decompress(buffer, file.name);

    // Check if result is a tar archive
    const format = detectFormat(data.buffer);
    const lname = file.name.toLowerCase();
    if (format === 'tar' || lname.includes('.tar')) {
        return await extractTar(data.buffer);
    }

    return new TextDecoder().decode(data);
}
