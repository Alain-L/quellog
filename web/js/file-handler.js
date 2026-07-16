// File handling utilities for quellog web app

import { wasmModule, setWasmModule, setWasmReady } from './state.js';

// 500 MB cap: empirical browser ceiling for the wasm pipeline under
// tinygo gc=leaking. J.log 500 MB and I.log 600 MB both succeed in
// Chrome, but we keep the conservative bound so the browser tab stays
// well under its 2 GB linear-memory limit across formats.
export const MAX_FILE_SIZE = 500 * 1024 * 1024;

// ===== Progress UI =====

export function setProgress(pct, text) {
    const bar = document.getElementById('progressBar');
    const label = document.getElementById('loadingText');
    if (bar) bar.style.width = pct + '%';
    if (label) label.textContent = text;
}

// ===== WASM Initialization =====

// Create fresh WASM instance (resets memory for gc=leaking workaround)
export async function initWasmInstance() {
    // Use window.wasmModule in standalone mode, module state otherwise
    const module = window.wasmModule || wasmModule;
    const go = new Go();
    const instance = await WebAssembly.instantiate(module, go.importObject);
    go.run(instance);
    // Wait for Go initialization
    await new Promise(r => setTimeout(r, 10));
}

// Load and compile WASM module on startup
export function loadWasm() {
    // Skip WASM loading in report mode (data is pre-embedded)
    if (window.REPORT_MODE) {
        return Promise.resolve();
    }

    // Skip WASM loading in standalone mode (WASM loaded by loader script)
    if (window.STANDALONE_MODE) {
        return Promise.resolve();
    }

    return fetch('quellog_tiny.wasm')
        .then(response => response.arrayBuffer())
        .then(buffer => WebAssembly.compile(buffer))
        .then(module => {
            setWasmModule(module);
            return initWasmInstance();
        })
        .then(() => {
            setWasmReady(true);
            // Update footer version now that WASM is loaded
            const versionEl = document.getElementById('quellog-version');
            if (versionEl) versionEl.textContent = 'quellog ' + quellogVersion();
        })
        .catch(err => {
            console.error('WASM load failed:', err);
            alert('Failed to load WASM module');
        });
}

// ===== Drag & Drop Setup =====

export function setupDragDrop(dropZone, fileInput, onFileSelected) {
    // Drag and drop handlers
    dropZone.addEventListener('dragover', e => {
        e.preventDefault();
        dropZone.classList.add('drag-over');
    });

    dropZone.addEventListener('dragleave', () => {
        dropZone.classList.remove('drag-over');
    });

    dropZone.addEventListener('drop', e => {
        e.preventDefault();
        dropZone.classList.remove('drag-over');
        if (e.dataTransfer.files[0]) {
            onFileSelected(e.dataTransfer.files[0]);
        }
    });

    // File input handler
    fileInput.addEventListener('change', e => {
        if (e.target.files[0]) {
            onFileSelected(e.target.files[0]);
        }
    });
}

// ===== UI State Helpers =====

export function showLoading(dropZone, loading, results) {
    dropZone.style.display = 'none';
    loading.classList.add('active');
    results.classList.remove('active');
}

export function hideLoading(loading) {
    loading.classList.remove('active');
}

export function showDropZone(dropZone) {
    dropZone.style.display = 'block';
}
