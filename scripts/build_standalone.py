#!/usr/bin/env python3
"""
Build standalone HTML with TinyGo WASM + full zstd compression.
All assets (CSS, JS, WASM) are zstd compressed except the bootstrap decoder (gzip).
"""

import base64
import gzip
import os
import re
import subprocess
import sys
import tempfile

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

def zstd_compress(content):
    """Compress string content with zstd -19"""
    with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
        f.write(content)
        tmp_path = f.name
    result = subprocess.run(['zstd', '-c', '-19', tmp_path], capture_output=True)
    os.unlink(tmp_path)
    return result.stdout

def zstd_compress_binary(data):
    """Compress binary content with zstd -19"""
    with tempfile.NamedTemporaryFile(mode='wb', suffix='.bin', delete=False) as f:
        f.write(data)
        tmp_path = f.name
    result = subprocess.run(['zstd', '-c', '-19', tmp_path], capture_output=True)
    os.unlink(tmp_path)
    return result.stdout

def main():
    print("Building standalone HTML (TinyGo + full ZSTD compression)...")
    print("=" * 60)

    # Read source files
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

    print(f"Source files:")
    print(f"  HTML:         {len(html_content):,} bytes")
    print(f"  CSS:          {len(css_raw):,} bytes")
    print(f"  app.js:       {len(app_js):,} bytes")
    print(f"  uPlot.js:     {len(uplot_js):,} bytes")
    print(f"  wasm_exec.js: {len(wasm_exec_js):,} bytes")
    print(f"  WASM:         {len(wasm_binary):,} bytes")
    print(f"  fzstd.min.js: {len(zstd_decoder_js):,} bytes")
    print()

    # === Step 1: Minify all text assets ===
    print("Minifying...")
    css_min = minify_css(css_raw)
    uplot_min = minify_js(uplot_js)
    wasm_exec_min = minify_js(wasm_exec_js)

    # Clean app.js for standalone mode
    app_js_cleaned = re.sub(
        r"// Compile and cache WASM module on startup.*?// DOM elements",
        "// DOM elements",
        app_js,
        flags=re.DOTALL
    )
    app_js_cleaned = app_js_cleaned.replace('if (!wasmReady)', 'if (!window.wasmReady)')
    app_js_cleaned = app_js_cleaned.replace('await initWasmInstance();', '// WASM already initialized')
    app_js_min = minify_js(app_js_cleaned)

    print(f"  CSS:          {len(css_raw):,} → {len(css_min):,} bytes")
    print(f"  uPlot.js:     {len(uplot_js):,} → {len(uplot_min):,} bytes")
    print(f"  wasm_exec.js: {len(wasm_exec_js):,} → {len(wasm_exec_min):,} bytes")
    print(f"  app.js:       {len(app_js):,} → {len(app_js_min):,} bytes")
    total_min = len(css_min) + len(uplot_min) + len(wasm_exec_min) + len(app_js_min)
    print(f"  Total text:   {total_min:,} bytes")
    print()

    # === Step 2: Compress with zstd ===
    print("Compressing with zstd -19...")
    css_zstd = zstd_compress(css_min)
    uplot_zstd = zstd_compress(uplot_min)
    wasm_exec_zstd = zstd_compress(wasm_exec_min)
    app_js_zstd = zstd_compress(app_js_min)
    wasm_zstd = zstd_compress_binary(wasm_binary)

    print(f"  CSS:          {len(css_min):,} → {len(css_zstd):,} bytes ({len(css_zstd)/len(css_min)*100:.1f}%)")
    print(f"  uPlot.js:     {len(uplot_min):,} → {len(uplot_zstd):,} bytes ({len(uplot_zstd)/len(uplot_min)*100:.1f}%)")
    print(f"  wasm_exec.js: {len(wasm_exec_min):,} → {len(wasm_exec_zstd):,} bytes ({len(wasm_exec_zstd)/len(wasm_exec_min)*100:.1f}%)")
    print(f"  app.js:       {len(app_js_min):,} → {len(app_js_zstd):,} bytes ({len(app_js_zstd)/len(app_js_min)*100:.1f}%)")
    print(f"  WASM:         {len(wasm_binary):,} → {len(wasm_zstd):,} bytes ({len(wasm_zstd)/len(wasm_binary)*100:.1f}%)")
    total_zstd = len(css_zstd) + len(uplot_zstd) + len(wasm_exec_zstd) + len(app_js_zstd) + len(wasm_zstd)
    print(f"  Total zstd:   {total_zstd:,} bytes")
    print()

    # === Step 3: Gzip the bootstrap decoder ===
    print("Gzip bootstrap decoder...")
    decoder_gzip = gzip.compress(zstd_decoder_js.encode('utf-8'), compresslevel=9)
    print(f"  fzstd.min.js: {len(zstd_decoder_js):,} → {len(decoder_gzip):,} bytes")
    print()

    # === Step 4: Base64 encode all payloads ===
    print("Base64 encoding...")
    css_b64 = base64.b64encode(css_zstd).decode('ascii')
    uplot_b64 = base64.b64encode(uplot_zstd).decode('ascii')
    wasm_exec_b64 = base64.b64encode(wasm_exec_zstd).decode('ascii')
    app_js_b64 = base64.b64encode(app_js_zstd).decode('ascii')
    wasm_b64 = base64.b64encode(wasm_zstd).decode('ascii')
    decoder_b64 = base64.b64encode(decoder_gzip).decode('ascii')

    total_b64 = len(css_b64) + len(uplot_b64) + len(wasm_exec_b64) + len(app_js_b64) + len(wasm_b64) + len(decoder_b64)
    print(f"  CSS:          {len(css_b64):,} chars")
    print(f"  uPlot:        {len(uplot_b64):,} chars")
    print(f"  wasm_exec:    {len(wasm_exec_b64):,} chars")
    print(f"  app.js:       {len(app_js_b64):,} chars")
    print(f"  WASM:         {len(wasm_b64):,} chars")
    print(f"  Decoder:      {len(decoder_b64):,} chars")
    print(f"  Total:        {total_b64:,} chars")
    print()

    # === Step 5: Extract and minify body HTML ===
    body_match = re.search(r'<body>(.*?)<script', html_content, re.DOTALL)
    body_content = body_match.group(1).strip() if body_match else ""
    body_min = re.sub(r'>\s+<', '><', body_content)
    body_min = re.sub(r'\s+', ' ', body_min)
    print(f"Body HTML: {len(body_content):,} → {len(body_min):,} bytes")
    print()

    # === Step 6: Build loader that decompresses and injects everything ===
    # Flow:
    # 1. Gunzip fzstd decoder (using native DecompressionStream)
    # 2. Use fzstd to decompress: CSS, uPlot, wasm_exec, app.js, WASM
    # 3. Inject <style> and <script> elements
    # 4. Initialize WASM
    loader_js = '''window.wasmReady=false;
const D=s=>Uint8Array.from(atob(s),c=>c.charCodeAt(0));
async function gunzip(d){const s=new DecompressionStream('gzip');const w=s.writable.getWriter();w.write(D(d));w.close();const r=s.readable.getReader();const chunks=[];while(true){const{done,value}=await r.read();if(done)break;chunks.push(value)}const res=new Uint8Array(chunks.reduce((a,c)=>a+c.length,0));let o=0;for(const c of chunks){res.set(c,o);o+=c.length}return res}
async function init(){try{
const dec=await gunzip(FZSTD_GZ);eval(new TextDecoder().decode(dec));
const td=new TextDecoder();
const css=td.decode(fzstd.decompress(D(CSS_ZST)));
const uplot=td.decode(fzstd.decompress(D(UPLOT_ZST)));
const exec=td.decode(fzstd.decompress(D(EXEC_ZST)));
const app=td.decode(fzstd.decompress(D(APP_ZST)));
const wasm=fzstd.decompress(D(WASM_ZST));
const st=document.createElement('style');st.textContent=css;document.head.appendChild(st);
const s1=document.createElement('script');s1.textContent=uplot;document.head.appendChild(s1);
eval(exec);
const go=new Go();const{instance}=await WebAssembly.instantiate(wasm,go.importObject);go.run(instance);
window.wasmReady=true;
if(typeof cacheWasmForWorkers==='function'){cacheWasmForWorkers(wasm.buffer,exec)}
const s2=document.createElement('script');s2.textContent=app;document.head.appendChild(s2);
console.log('[quellog] Ready:',quellogVersion());
}catch(e){console.error(e);alert('Load error: '+e.message)}}
init();'''

    loader_min = minify_js(loader_js)
    print(f"Loader: {len(loader_js):,} → {len(loader_min):,} bytes")

    # === Step 7: Build final HTML ===
    standalone_html = f'''<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"><title>quellog</title></head><body>{body_min}<script>
const CSS_ZST="{css_b64}";
const UPLOT_ZST="{uplot_b64}";
const EXEC_ZST="{wasm_exec_b64}";
const APP_ZST="{app_js_b64}";
const WASM_ZST="{wasm_b64}";
const FZSTD_GZ="{decoder_b64}";
{loader_min}</script></body></html>'''

    # Write output
    output_path = os.path.join(WEB_DIR, "quellog.html")
    with open(output_path, 'w') as f:
        f.write(standalone_html)

    size_bytes = os.path.getsize(output_path)
    print()
    print("=" * 60)
    print(f"Output: {output_path}")
    print(f"Final size: {size_bytes:,} bytes ({size_bytes/1024:.0f} KB)")

if __name__ == "__main__":
    main()
