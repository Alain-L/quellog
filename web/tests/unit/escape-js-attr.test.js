// escForJsAttr: a value from the logs is embedded as a JS string
// literal inside an onclick attribute (the Copy buttons). Whatever it holds,
// the handler must give back exactly that string and run nothing else.
// esc() needs document.createElement, so a minimal fake DOM stands in: it
// escapes & < > like textContent → innerHTML does.
import { test } from 'node:test';
import assert from 'node:assert/strict';

globalThis.document = {
    createElement() {
        return {
            set textContent(v) { this.html = String(v).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); },
            get innerHTML() { return this.html; },
        };
    },
};
const { escForJsAttr } = await import('../../js/utils.js');

// What the browser does: decode the attribute's entities, then run the
// handler, here reduced to evaluating the string literal.
function roundTrip(value) {
    const attr = "x = '" + escForJsAttr(value) + "'";
    const handler = attr.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
    let x;
    let ran = false;
    globalThis.__pwned = () => { ran = true; };
    eval(handler); // eslint-disable-line no-eval
    delete globalThis.__pwned;
    return { x, ran };
}

for (const value of [
    "plain text",
    "it's quoted",
    "back\\slash",
    "\\'); __pwned(); ('",
    "\\\\'); __pwned(); //",
    'double " quote',
    "new\nline and\r\nCRLF",
    "</script><img src=x onerror=__pwned()>",
    "E'\\''",
]) {
    test(`escForJsAttr keeps ${JSON.stringify(value)} inside the string`, () => {
        const { x, ran } = roundTrip(value);
        assert.equal(ran, false);
        assert.equal(x, value);
    });
}
