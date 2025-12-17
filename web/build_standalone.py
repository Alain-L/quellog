#!/usr/bin/env python3
"""
Build standalone HTML for quellog WASM viewer.

Reads from:
  - index.html (template)
  - styles.css
  - app.js
  - uplot.min.js
  - fzstd.min.js
  - wasm_exec_tiny.js
  - quellog_tiny.wasm

Outputs:
  - quellog.html (standalone, all assets inlined)
"""

import re
import subprocess
import base64
import gzip
import os

WASM_DIR = os.path.dirname(os.path.abspath(__file__))

def read_file(path):
    with open(os.path.join(WASM_DIR, path), 'r') as f:
        return f.read()

def read_binary(path):
    with open(os.path.join(WASM_DIR, path), 'rb') as f:
        return f.read()

def minify_js(js):
    """Minimal JS minification - remove comments, preserve template literals."""
    # Remove multi-line comments
    js = re.sub(r'/\*[\s\S]*?\*/', '', js)
    # Remove single-line comments (but not in strings)
    lines = []
    for line in js.split('\n'):
        if '`' not in line and '//' in line:
            in_string = False
            quote_char = None
            result = []
            i = 0
            while i < len(line):
                c = line[i]
                if not in_string and c in '"\'':
                    in_string = True
                    quote_char = c
                elif in_string and c == quote_char and (i == 0 or line[i-1] != '\\'):
                    in_string = False
                elif not in_string and line[i:i+2] == '//':
                    break
                result.append(c)
                i += 1
            line = ''.join(result)
        lines.append(line)
    return '\n'.join(lines)

def minify_css(css):
    """Basic CSS minification."""
    css = re.sub(r'/\*[\s\S]*?\*/', '', css)
    css = re.sub(r'\s+', ' ', css)
    css = re.sub(r'\s*([{};:,>~+])\s*', r'\1', css)
    return css.strip()

def build_standalone():
    print("Building standalone HTML...")
    print(f"Working directory: {WASM_DIR}")

    # 1. Compress WASM with zstd
    wasm_path = os.path.join(WASM_DIR, 'quellog_tiny.wasm')
    print(f"\n[1/5] Compressing WASM...")
    wasm_data = read_binary('quellog_tiny.wasm')
    wasm_zst = subprocess.run(
        ['zstd', '-19', '-c', wasm_path],
        capture_output=True, check=True
    ).stdout
    wasm_b64 = base64.b64encode(wasm_zst).decode('ascii')
    print(f"  quellog_tiny.wasm: {len(wasm_data):,} → {len(wasm_zst):,} (zstd) → {len(wasm_b64):,} (b64)")

    # 2. Read and process assets
    print(f"\n[2/5] Reading assets...")

    uplot_js = read_file('uplot.min.js')
    print(f"  uplot.min.js: {len(uplot_js):,} bytes")

    fzstd_js = read_file('fzstd.min.js')
    fzstd_gz = gzip.compress(fzstd_js.encode('utf-8'), compresslevel=9)
    fzstd_b64 = base64.b64encode(fzstd_gz).decode('ascii')
    print(f"  fzstd.min.js: {len(fzstd_js):,} → {len(fzstd_gz):,} (gzip) → {len(fzstd_b64):,} (b64)")

    wasm_exec = read_file('wasm_exec_tiny.js')
    wasm_exec_min = minify_js(wasm_exec)
    print(f"  wasm_exec_tiny.js: {len(wasm_exec):,} → {len(wasm_exec_min):,} (minified)")

    css = read_file('styles.css')
    css_min = minify_css(css)
    print(f"  styles.css: {len(css):,} → {len(css_min):,} (minified)")

    app_js = read_file('app.js')
    app_js_min = minify_js(app_js)
    print(f"  app.js: {len(app_js):,} → {len(app_js_min):,} (minified)")

    # 3. Read HTML template
    print(f"\n[3/5] Reading HTML template...")
    html = read_file('index.html')
    print(f"  index.html: {len(html):,} bytes")

    # 4. Build standalone
    print(f"\n[4/5] Building standalone...")

    # Create the WASM loader script
    loader_script = f'''
// Standalone loader - embedded assets
// uPlot charting library
{uplot_js}

const WASM_ZST_B64="{wasm_b64}";
const FZSTD_GZ_B64="{fzstd_b64}";

// Decompress and eval fzstd
(function(){{
    const gz=Uint8Array.from(atob(FZSTD_GZ_B64),c=>c.charCodeAt(0));
    const ds=new DecompressionStream('gzip');
    const w=ds.writable.getWriter();
    w.write(gz);w.close();
    new Response(ds.readable).text().then(eval);
}})();

// TinyGo wasm_exec
{wasm_exec_min}

// Initialize WASM (standalone mode)
window.wasmReady=false;
window.wasmModule=null;

// Reinitialize WASM instance (resets memory for gc=leaking)
window.reinitWasm=async function(){{
    if(!window.wasmModule)return;
    const go=new Go();
    const instance=await WebAssembly.instantiate(window.wasmModule,go.importObject);
    go.run(instance);
    console.log('[quellog] WASM reinitialized');
}};

(async function(){{
    // Wait for fzstd to be available
    while(typeof fzstd==='undefined')await new Promise(r=>setTimeout(r,50));
    try{{
        const wzst=Uint8Array.from(atob(WASM_ZST_B64),c=>c.charCodeAt(0));
        const wb=fzstd.decompress(wzst);
        // Compile first, then instantiate (needed for reinitWasm to work)
        window.wasmModule=await WebAssembly.compile(wb);
        const go=new Go();
        const instance=await WebAssembly.instantiate(window.wasmModule,go.importObject);
        go.run(instance);
        window.wasmReady=true;
        console.log('[quellog] WASM ready (standalone)');
    }}catch(e){{
        console.error('[quellog] WASM init failed:',e);
        alert('WASM initialization failed: '+e.message);
    }}
}})();
'''

    # Modify app.js for standalone mode
    app_js_standalone = app_js_min
    # Replace wasmReady check to use window.wasmReady
    app_js_standalone = app_js_standalone.replace('if (!wasmReady)', 'if (!window.wasmReady)')
    # Replace wasmModule usage to use window.wasmModule
    app_js_standalone = app_js_standalone.replace('WebAssembly.instantiate(wasmModule,', 'WebAssembly.instantiate(window.wasmModule,')
    # Remove the fetch WASM block
    wasm_init_pattern = r"fetch\('quellog\.wasm'\)[\s\S]*?Failed to load WASM module[\s\S]*?\}\);"
    app_js_standalone = re.sub(wasm_init_pattern, '// WASM init handled by standalone loader', app_js_standalone)

    # Build the standalone HTML
    standalone = f'''<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>quellog - PostgreSQL Log Analyzer</title>
<style>{css_min}</style>
</head>
<body>'''

    # Extract body content from template (everything between <body> and <!-- Scripts -->)
    body_match = re.search(r'<body>([\s\S]*?)<!-- Scripts -->', html)
    if body_match:
        standalone += body_match.group(1).strip()

    standalone += f'''
<script>
{loader_script}

{app_js_standalone}
</script>
</body>
</html>'''

    # 5. Write output
    print(f"\n[5/5] Writing output...")
    output_path = os.path.join(WASM_DIR, 'quellog.html')
    with open(output_path, 'w') as f:
        f.write(standalone)

    print(f"\n✓ Output: {output_path}")
    print(f"✓ Size: {len(standalone):,} bytes ({len(standalone)/1024:.1f} KB)")

if __name__ == '__main__':
    build_standalone()
