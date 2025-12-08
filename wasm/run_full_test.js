const fs = require('fs');
const path = require('path');

require('./wasm_exec.js');

async function testFile(logPath) {
    const fileBuffer = fs.readFileSync(logPath);
    const wasmBuffer = fs.readFileSync(path.join(__dirname, 'quellog_bytes.wasm'));
    const sizeMB = (fileBuffer.length / 1024 / 1024).toFixed(0);

    console.log(`\n${path.basename(logPath)} (${sizeMB} MB)`);
    console.log('-'.repeat(40));

    const go = new global.Go();
    const result = await WebAssembly.instantiate(wasmBuffer, go.importObject);
    const instance = result.instance;
    go.run(instance);

    while (!global.wasmAlloc) {
        await new Promise(r => setTimeout(r, 10));
    }

    const ptr = global.wasmAlloc(fileBuffer.length);
    const wasmMem = instance.exports.memory || instance.exports.mem;
    new Uint8Array(wasmMem.buffer, ptr, fileBuffer.length).set(fileBuffer);

    const start = Date.now();
    try {
        const memUsed = global.testParser(ptr, fileBuffer.length);
        const elapsed = ((Date.now() - start) / 1000).toFixed(2);
        console.log(`Time: ${elapsed}s, Memory: ${memUsed} MB`);
        return { success: true, time: elapsed, mem: memUsed };
    } catch (e) {
        const elapsed = ((Date.now() - start) / 1000).toFixed(2);
        console.log(`OOM after ${elapsed}s: ${e.message}`);
        return { success: false, time: elapsed };
    }
}

async function run() {
    const file = process.argv[2] || '/Users/alain/DALIBO/dev/projects/quellog/_random_logs/samples/I1.log';
    await testFile(file);
}

run();
