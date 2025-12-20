#!/usr/bin/env python3
"""
Build standalone HTML with TinyGo WASM + zstd compression.
Always uses TinyGo build for smallest size.
"""

import base64
import gzip
import os
import re
import subprocess
import sys

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
WASM_DIR = os.path.join(PROJECT_DIR, "wasm")
WEB_DIR = os.path.join(PROJECT_DIR, "web")
ZSTD_DECODER = os.path.join(SCRIPT_DIR, "fzstd.min.js")
UPLOT_JS = os.path.join(SCRIPT_DIR, "uplot.min.js")

def minify_css(css):
    """Simple CSS minification"""
    css = re.sub(r'/\*.*?\*/', '', css, flags=re.DOTALL)
    css = re.sub(r'\s+', ' ', css)
    css = re.sub(r'\s*([{};:,])\s*', r'\1', css)
    css = re.sub(r';\s*}', '}', css)
    return css.strip()

def minify_js(js):
    """Simple JS minification (conservative)"""
    js = re.sub(r'(?<!:)//[^\n]*', '', js)
    js = re.sub(r'/\*.*?\*/', '', js, flags=re.DOTALL)
    js = re.sub(r'\n\s*\n', '\n', js)
    js = re.sub(r'^\s+', '', js, flags=re.MULTILINE)
    return js.strip()

def main():
    print("Building standalone HTML (TinyGo + ZSTD)...")
    print("=" * 50)

    # Read source files from web/ directory
    html_path = os.path.join(WEB_DIR, "index.html")
    css_path = os.path.join(WEB_DIR, "styles.css")
    app_path = os.path.join(WEB_DIR, "app.js")
    wasm_path = os.path.join(WASM_DIR, "quellog_tiny.wasm")
    wasm_exec_path = os.path.join(WEB_DIR, "wasm_exec_tiny_browser.js")

    if not os.path.exists(wasm_path):
        print(f"ERROR: TinyGo WASM not found: {wasm_path}")
        print("Build it with: cd wasm && tinygo build -o quellog_tiny.wasm -target wasm -gc=leaking -no-debug ./main.go")
        sys.exit(1)

    if not os.path.exists(wasm_exec_path):
        print(f"ERROR: wasm_exec.js not found: {wasm_exec_path}")
        sys.exit(1)

    with open(html_path, 'r') as f:
        html_content = f.read()
    with open(css_path, 'r') as f:
        css_raw = f.read()
    with open(app_path, 'r') as f:
        app_js = f.read()
    with open(wasm_path, 'rb') as f:
        wasm_binary = f.read()
    with open(wasm_exec_path, 'r') as f:
        wasm_exec_js = f.read()
    with open(ZSTD_DECODER, 'r') as f:
        zstd_decoder_js = f.read()
    with open(UPLOT_JS, 'r') as f:
        uplot_js = f.read()

    print(f"Source HTML:      {len(html_content):,} bytes")
    print(f"Source CSS:       {len(css_raw):,} bytes")
    print(f"Source JS:        {len(app_js):,} bytes")
    print(f"WASM binary:      {len(wasm_binary):,} bytes")
    print(f"wasm_exec.js:     {len(wasm_exec_js):,} bytes")
    print(f"Zstd decoder:     {len(zstd_decoder_js):,} bytes")
    print(f"uPlot.js:         {len(uplot_js):,} bytes")
    print()

    # Compress WASM with zstd level 19 (max without ultra)
    result = subprocess.run(['zstd', '-c', '-19', wasm_path], capture_output=True)
    wasm_zstd = result.stdout
    print(f"WASM Zstd:        {len(wasm_zstd):,} bytes ({len(wasm_zstd)/len(wasm_binary)*100:.1f}%)")

    # Gzip the decoder (needed first to decompress zstd)
    decoder_gzip = gzip.compress(zstd_decoder_js.encode('utf-8'), compresslevel=9)
    print(f"Decoder gzipped:  {len(decoder_gzip):,} bytes")

    # Minify wasm_exec before compression
    wasm_exec_min = minify_js(wasm_exec_js)
    print(f"wasm_exec min:    {len(wasm_exec_min):,} bytes ({len(wasm_exec_min)/len(wasm_exec_js)*100:.1f}%)")

    # Zstd compress wasm_exec (will be decompressed after decoder is loaded)
    import tempfile
    with tempfile.NamedTemporaryFile(mode='w', suffix='.js', delete=False) as f:
        f.write(wasm_exec_min)
        tmp_path = f.name
    result_exec = subprocess.run(['zstd', '-c', '-19', tmp_path], capture_output=True)
    wasm_exec_zstd = result_exec.stdout
    os.unlink(tmp_path)
    print(f"wasm_exec zstd:   {len(wasm_exec_zstd):,} bytes")

    # Base64 encode
    wasm_b64 = base64.b64encode(wasm_zstd).decode('ascii')
    decoder_b64 = base64.b64encode(decoder_gzip).decode('ascii')
    wasm_exec_b64 = base64.b64encode(wasm_exec_zstd).decode('ascii')

    print()
    print(f"Base64 payload:")
    print(f"  WASM:    {len(wasm_b64):,} chars")
    print(f"  Decoder: {len(decoder_b64):,} chars")
    print(f"  Exec:    {len(wasm_exec_b64):,} chars")
    print(f"  Total:   {len(wasm_b64) + len(decoder_b64) + len(wasm_exec_b64):,} chars")

    # Minify CSS
    css_min = minify_css(css_raw)
    print()
    print(f"CSS: {len(css_raw):,} → {len(css_min):,} bytes ({(1-len(css_min)/len(css_raw))*100:.0f}% reduction)")

    # Extract body (between <body> and first <script)
    body_match = re.search(r'<body>(.*?)<script', html_content, re.DOTALL)
    body_content = body_match.group(1).strip() if body_match else ""
    body_min = re.sub(r'>\s+<', '><', body_content)
    body_min = re.sub(r'\s+', ' ', body_min)
    print(f"Body HTML: {len(body_content):,} → {len(body_min):,} bytes")

    # Minify uPlot (already minified, just in case)
    uplot_min = minify_js(uplot_js)
    print(f"uPlot min:        {len(uplot_min):,} bytes")

    # Remove only the WASM compile/fetch block from app.js (preserve chart management)
    app_js_cleaned = re.sub(
        r"// Compile and cache WASM module on startup.*?// DOM elements",
        "// DOM elements",
        app_js,
        flags=re.DOTALL
    )
    app_js_cleaned = app_js_cleaned.replace('if (!wasmReady)', 'if (!window.wasmReady)')
    # Replace initWasmInstance() call with no-op (WASM is initialized once by loader)
    app_js_cleaned = app_js_cleaned.replace('await initWasmInstance();', '// WASM already initialized')
    app_js_min = minify_js(app_js_cleaned)
    print(f"App JS: {len(app_js):,} → {len(app_js_min):,} bytes ({(1-len(app_js_min)/len(app_js))*100:.0f}% reduction)")

    # Build minimal loader with zstd decompression
    # fzstd decoder is gzipped, wasm_exec and WASM are zstd compressed
    loader_js = '''window.wasmReady=false;async function gunzip(d){const c=Uint8Array.from(atob(d),c=>c.charCodeAt(0));const s=new DecompressionStream('gzip');const w=s.writable.getWriter();w.write(c);w.close();const r=s.readable.getReader();const chunks=[];while(true){const{done,value}=await r.read();if(done)break;chunks.push(value)}const res=new Uint8Array(chunks.reduce((a,c)=>a+c.length,0));let o=0;for(const c of chunks){res.set(c,o);o+=c.length}return res}async function initWasm(){try{const dec=await gunzip(ZSTD_DEC_GZ_B64);eval(new TextDecoder().decode(dec));const execZst=Uint8Array.from(atob(WASM_EXEC_ZST_B64),c=>c.charCodeAt(0));const execCode=new TextDecoder().decode(fzstd.decompress(execZst));eval(execCode);const wzst=Uint8Array.from(atob(WASM_ZST_B64),c=>c.charCodeAt(0));const wb=fzstd.decompress(wzst);const go=new Go();const{instance}=await WebAssembly.instantiate(wb,go.importObject);go.run(instance);window.wasmReady=true;if(typeof cacheWasmForWorkers==='function'){cacheWasmForWorkers(wb.buffer,execCode)}console.log('Ready:',quellogVersion())}catch(e){console.error(e);alert('Load error: '+e.message)}}initWasm();'''

    print(f"Loader JS: {len(loader_js):,} bytes (minified)")

    # Build standalone HTML (uPlot + loader + app)
    standalone_html = f'''<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"><title>quellog</title><style>{css_min}</style></head><body>{body_min}<script>{uplot_min}</script><script>const WASM_ZST_B64="{wasm_b64}";const ZSTD_DEC_GZ_B64="{decoder_b64}";const WASM_EXEC_ZST_B64="{wasm_exec_b64}";{loader_js}{app_js_min}</script></body></html>'''

    # Write to standalone file
    output_path = os.path.join(WEB_DIR, "quellog.html")
    with open(output_path, 'w') as f:
        f.write(standalone_html)

    size_bytes = os.path.getsize(output_path)
    print()
    print("=" * 50)
    print(f"Output: {output_path}")
    print(f"Final size: {size_bytes:,} bytes ({size_bytes/1024:.0f} KB)")

if __name__ == "__main__":
    main()
