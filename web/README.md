# quellog Web

Version WebAssembly de quellog pour exécution dans le navigateur.

## Structure

```
web/
├── index.html              # Template dev (imports externes)
├── styles.css              # Styles CSS
├── app.js                  # Application JavaScript
├── uplot.min.js            # Librairie charts
├── fzstd.min.js            # Décodeur zstd
├── wasm_exec_tiny.js       # Runtime TinyGo WASM
├── quellog.html            # Standalone (tout embarqué)
├── build_standalone.py     # Script de build
├── main.go                 # Point d'entrée WASM
├── quellog_tiny.wasm       # Build TinyGo
└── quellog.wasm            # Build Go standard
```

## Développement

```bash
cd web
python3 -m http.server 8080
# Ouvrir http://localhost:8080/index.html
```

## Build

### TinyGo WASM (recommandé)

```bash
cd web
tinygo build -o quellog_tiny.wasm -target wasm -gc=leaking -no-debug ./main.go
```

### Go standard

```bash
cd web
GOOS=js GOARCH=wasm go build -o quellog.wasm .
```

### Standalone HTML

```bash
cd web
python3 build_standalone.py
# Output: quellog.html (~586 KB)
```

Le standalone embarque tout dans un seul fichier HTML :
- WASM TinyGo compressé zstd + base64
- Décodeur fzstd compressé gzip + base64
- Runtime wasm_exec minifié
- CSS/JS minifiés
- uPlot pour les charts

## API JavaScript

```javascript
// Parser un log
const json = quellogParse(logContent);
const data = JSON.parse(json);

// Version
quellogVersion()  // "0.2.0-wasm"
```

## Limites

- **Taille fichier** : ~1.5 GB max (configurable)
- **Mémoire** : ~2-4 GB par onglet navigateur
- **GC** : TinyGo avec `-gc=leaking` ne libère pas la mémoire

Pour les gros fichiers (>500 MB), utiliser la CLI native.
