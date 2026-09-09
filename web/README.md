# quellog Web

Browser-based version of quellog using WebAssembly.

## Structure

```
web/
├── index.html              # HTML template
├── styles.css              # CSS styles
├── app.js                  # Entry point (ES module)
├── js/                     # JS modules (utils, state, theme, compression,
│   │                       #   filters, file-handler, charts, binning,
│   │                       #   format, period-nav, report-filter, …)
│   ├── components/         # UI components (ql-dropdown, ql-modal, ql-tabs, ql-tooltip)
│   └── sections/           # Per-section renderers (summary, sql, locks, …)
├── uplot.min.js            # Chart library
├── fzstd.min.js            # Zstd decompressor
├── bundle.go               # esbuild bundler (go:generate target)
├── embed.go                # //go:embed of the bundled assets
├── standalone.go           # Standalone single-file report builder
├── report.tmpl             # HTML report templates (+ report_split.tmpl)
├── app.bundle.js           # Generated: esbuild IIFE bundle
├── quellog_tiny.wasm       # Generated: TinyGo WASM module
├── wasm_exec_tiny.js       # TinyGo JS runtime
└── wasm/
    └── main.go             # WASM entry point
```

## Development

```bash
# Bundle JS and rebuild binary
go generate ./web/...
go build -o bin/quellog .

# Or use the Makefile
make build
```

## Build

The JS modules are bundled into a single IIFE file (`app.bundle.js`) using esbuild (Go API) via `go generate`. Assets are embedded into the Go binary with `//go:embed`.

No Node.js or Python required to build (the web test suite, `make test-web`, does use npm).

## JavaScript API

```javascript
// Parse log content (string); optional filters JSON (begin/end/db/user/app)
const json = quellogParse(logContent, filtersJson);

// Parse log content (binary, avoids UTF-8 round-trip)
const json = quellogParseBytes(uint8Array, filtersJson);

// Split into per-period report blobs
const json = quellogSplitBytes(uint8Array, intervalSeconds, filename, filtersJson);

// Version
quellogVersion()
```

## Limitations

- **File size**: ~1.5 GB max (browser memory dependent)
- **Memory**: ~2-4 GB per browser tab

For large files (>500 MB), use the native CLI.
