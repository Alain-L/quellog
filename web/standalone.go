//go:build ignore

// Build standalone HTML for quellog WASM viewer.
// Run with: go run web/standalone.go
// Or via: make standalone
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var webDir string

func init() {
	// Find web directory relative to this script
	exe, _ := os.Executable()
	webDir = filepath.Dir(exe)
	// When run with "go run", we need to find the actual web directory
	if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		// Try current directory
		if cwd, err := os.Getwd(); err == nil {
			if _, err := os.Stat(filepath.Join(cwd, "web", "index.html")); err == nil {
				webDir = filepath.Join(cwd, "web")
			} else if _, err := os.Stat(filepath.Join(cwd, "index.html")); err == nil {
				webDir = cwd
			}
		}
	}
}

func readFile(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(webDir, name))
	return string(data), err
}

func readBinary(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(webDir, name))
}

func minifyJS(js string) string {
	// Remove multi-line comments
	js = regexp.MustCompile(`/\*[\s\S]*?\*/`).ReplaceAllString(js, "")
	// Remove single-line comments (simple approach - not in strings)
	lines := strings.Split(js, "\n")
	var result []string
	for _, line := range lines {
		if !strings.Contains(line, "`") && strings.Contains(line, "//") {
			// Simple comment removal (not perfect but good enough for wasm_exec)
			if idx := strings.Index(line, "//"); idx >= 0 {
				// Check if // is inside a string (very basic check)
				beforeComment := line[:idx]
				singleQuotes := strings.Count(beforeComment, "'") - strings.Count(beforeComment, "\\'")
				doubleQuotes := strings.Count(beforeComment, "\"") - strings.Count(beforeComment, "\\\"")
				if singleQuotes%2 == 0 && doubleQuotes%2 == 0 {
					line = line[:idx]
				}
			}
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func minifyCSS(css string) string {
	// Remove comments
	css = regexp.MustCompile(`/\*[\s\S]*?\*/`).ReplaceAllString(css, "")
	// Collapse whitespace
	css = regexp.MustCompile(`\s+`).ReplaceAllString(css, " ")
	// Remove space around punctuation
	css = regexp.MustCompile(`\s*([{};:,>~+])\s*`).ReplaceAllString(css, "$1")
	return strings.TrimSpace(css)
}

func gzipBase64(data string) (string, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	gz.Write([]byte(data))
	gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func zstdBase64File(filename string) (string, int, error) {
	// Use external zstd command for best compression (level 19)
	cmd := exec.Command("zstd", "-19", "-c", filename)
	output, err := cmd.Output()
	if err != nil {
		return "", 0, fmt.Errorf("zstd command failed: %w", err)
	}
	return base64.StdEncoding.EncodeToString(output), len(output), nil
}

func main() {
	fmt.Println("Building standalone HTML...")
	fmt.Printf("Working directory: %s\n", webDir)

	// 1. Compress WASM with zstd
	fmt.Println("\n[1/5] Compressing WASM...")
	wasmData, err := readBinary("quellog_tiny.wasm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	wasmPath := filepath.Join(webDir, "quellog_tiny.wasm")
	wasmB64, zstdSize, err := zstdBase64File(wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR compressing WASM: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  quellog_tiny.wasm: %d → %d (zstd) → %d (b64)\n", len(wasmData), zstdSize, len(wasmB64))

	// 2. Read and process assets
	fmt.Println("\n[2/5] Reading assets...")

	uplotJS, err := readFile("uplot.min.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  uplot.min.js: %d bytes\n", len(uplotJS))

	fzstdJS, err := readFile("fzstd.min.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fzstdB64, err := gzipBase64(fzstdJS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	gzData, _ := base64.StdEncoding.DecodeString(fzstdB64)
	fmt.Printf("  fzstd.min.js: %d → %d (gzip) → %d (b64)\n", len(fzstdJS), len(gzData), len(fzstdB64))

	wasmExec, err := readFile("wasm_exec_tiny.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	wasmExecMin := minifyJS(wasmExec)
	fmt.Printf("  wasm_exec_tiny.js: %d → %d (minified)\n", len(wasmExec), len(wasmExecMin))

	css, err := readFile("styles.css")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	cssMin := minifyCSS(css)
	fmt.Printf("  styles.css: %d → %d (minified)\n", len(css), len(cssMin))

	appJS, err := readFile("app.bundle.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: app.bundle.js not found.\n")
		fmt.Fprintln(os.Stderr, "Run 'go generate ./web/...' first to bundle the JS modules.")
		os.Exit(1)
	}
	fmt.Printf("  app.bundle.js: %d bytes (pre-bundled)\n", len(appJS))

	// 3. Read HTML template
	fmt.Println("\n[3/5] Reading HTML template...")
	html, err := readFile("index.html")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  index.html: %d bytes\n", len(html))

	// 4. Build standalone
	fmt.Println("\n[4/5] Building standalone...")

	// Create the WASM loader script
	loaderScript := fmt.Sprintf(`
// Standalone loader - embedded assets
// uPlot charting library
%s

const WASM_ZST_B64="%s";
const FZSTD_GZ_B64="%s";

// Decompress and eval fzstd
(function(){
    const gz=Uint8Array.from(atob(FZSTD_GZ_B64),c=>c.charCodeAt(0));
    const ds=new DecompressionStream('gzip');
    const w=ds.writable.getWriter();
    w.write(gz);w.close();
    new Response(ds.readable).text().then(eval);
})();

// TinyGo wasm_exec
%s

// Initialize WASM (standalone mode)
window.STANDALONE_MODE=true;
window.wasmReady=false;
window.wasmModule=null;

// Reinitialize WASM instance (resets memory for gc=leaking)
window.reinitWasm=async function(){
    if(!window.wasmModule)return;
    const go=new Go();
    const instance=await WebAssembly.instantiate(window.wasmModule,go.importObject);
    go.run(instance);
    console.log('[quellog] WASM reinitialized');
};

(async function(){
    // Wait for fzstd to be available
    while(typeof fzstd==='undefined')await new Promise(r=>setTimeout(r,50));
    try{
        const wzst=Uint8Array.from(atob(WASM_ZST_B64),c=>c.charCodeAt(0));
        const wb=fzstd.decompress(wzst);
        // Compile first, then instantiate (needed for reinitWasm to work)
        window.wasmModule=await WebAssembly.compile(wb);
        const go=new Go();
        const instance=await WebAssembly.instantiate(window.wasmModule,go.importObject);
        go.run(instance);
        window.wasmReady=true;
        console.log('[quellog] WASM ready (standalone)');
        var ve=document.getElementById('quellog-version');
        if(ve&&typeof quellogVersion==='function')ve.textContent='quellog '+quellogVersion();
    }catch(e){
        console.error('[quellog] WASM init failed:',e);
        alert('WASM initialization failed: '+e.message);
    }
})();
`, uplotJS, wasmB64, fzstdB64, wasmExecMin)

	// Extract body content from template
	bodyRe := regexp.MustCompile(`(?s)<body>(.*?)<!-- Scripts -->`)
	bodyMatch := bodyRe.FindStringSubmatch(html)
	bodyContent := ""
	if len(bodyMatch) > 1 {
		bodyContent = strings.TrimSpace(bodyMatch[1])
	}

	// Build the standalone HTML
	standalone := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>quellog - PostgreSQL Log Analyzer</title>
<style>%s</style>
</head>
<body>%s
<script>
%s

%s
</script>
</body>
</html>`, cssMin, bodyContent, loaderScript, appJS)

	// 5. Write output
	fmt.Println("\n[5/5] Writing output...")
	outputPath := filepath.Join(webDir, "quellog.html")
	if err := os.WriteFile(outputPath, []byte(standalone), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Output: %s\n", outputPath)
	fmt.Printf("✓ Size: %d bytes (%.1f KB)\n", len(standalone), float64(len(standalone))/1024)
}
