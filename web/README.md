# quellog Web

Browser-based version of quellog using WebAssembly.

## Structure

```
web/
├── index.html              # HTML template
├── styles.css              # CSS styles
├── app.js                  # Entry point (ES module)
├── js/                     # JS modules
│   ├── utils.js
│   ├── state.js
│   ├── theme.js
│   ├── compression.js
│   ├── filters.js
│   ├── file-handler.js
│   └── charts.js
├── uplot.min.js            # Chart library
├── fzstd.min.js            # Zstd decompressor
├── app.bundle.js           # Generated: esbuild IIFE bundle
└── wasm/
    └── main.go             # WASM entry point
```

## Development

```bash
# Bundle JS and rebuild binary
go generate ./output/...
go build -o bin/quellog .

# Or use the Makefile
make build
```

## Build

The JS modules are bundled into a single IIFE file (`app.bundle.js`) using esbuild (Go API) via `go generate`. Assets are embedded into the Go binary with `//go:embed`.

No Node.js or Python required.

## JavaScript API

```javascript
// Parse log content (string)
const json = quellogParse(logContent);

// Parse log content (binary, avoids UTF-8 round-trip)
const json = quellogParseBytes(uint8Array);

// Version
quellogVersion()
```

## Limitations

- **File size**: ~1.5 GB max (browser memory dependent)
- **Memory**: ~2-4 GB per browser tab

For large files (>500 MB), use the native CLI.
