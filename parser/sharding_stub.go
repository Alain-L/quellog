//go:build js

package parser

// SupportsPIDSharding always reports false in the WASM build. The WASM path
// analyzes single-pass (no PID-sharding), and the compressed/archive format
// sniffing the native implementation relies on is not compiled here. It is
// never called in WASM (only cmd, which is native-only, uses it); the stub just
// satisfies the linker.
func SupportsPIDSharding(string) bool { return false }
