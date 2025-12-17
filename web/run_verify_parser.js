const fs = require('fs');
const path = require('path');

require('./wasm_exec.js');

async function testFile(logPath) {
    const fileBuffer = fs.readFileSync(logPath);
    const wasmBuffer = fs.readFileSync(path.join(__dirname, 'quellog_bytes.wasm'));
    const sizeMB = (fileBuffer.length / 1024 / 1024).toFixed(1);

    console.log(`\n${path.basename(logPath)} (${sizeMB} MB)`);
    console.log('-'.repeat(40));

    const go = new global.Go();
    const result = await WebAssembly.instantiate(wasmBuffer, go.importObject);
    const instance = result.instance;
    go.run(instance);

    // Wait for WASM to initialize
    while (!global.wasmAlloc) {
        await new Promise(r => setTimeout(r, 10));
    }

    const ptr = global.wasmAlloc(fileBuffer.length);
    const wasmMem = instance.exports.memory || instance.exports.mem;
    new Uint8Array(wasmMem.buffer, ptr, fileBuffer.length).set(fileBuffer);

    try {
        const memUsed = global.testParser(ptr, fileBuffer.length);
        return memUsed;
    } catch (e) {
        console.log(`OOM: ${e.message}`);
        return -1;
    }
}

async function run() {
    console.log('='.repeat(50));
    console.log('PARSER VERIFICATION (with official parser)');
    console.log('='.repeat(50));

    const files = [
        '/Users/alain/DALIBO/dev/projects/quellog/_random_logs/truncated_oom/I1_200M.log',
        '/Users/alain/DALIBO/dev/projects/quellog/_random_logs/samples/B.log',
    ];

    for (const f of files) {
        if (fs.existsSync(f)) {
            await testFile(f);
        }
    }

    console.log('\n' + '='.repeat(50));
}

run();
