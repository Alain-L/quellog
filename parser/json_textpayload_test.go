package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJSONTextPayloadNoAliasing guards against a buffer-aliasing
// regression in the Google Cloud SQL (textPayload) JSON path. gjson's
// String() sub-slices its input for unescaped strings; the sequential
// JSON reader feeds it unsafeString(scanner.Bytes()), so a textPayload
// returned verbatim would alias the reusable scanner buffer and get
// overwritten on the next buffer refill. The file here is sized well
// past the 4 MB scanner window to force refills mid-parse; every
// emitted message must still carry its own marker.
func TestJSONTextPayloadNoAliasing(t *testing.T) {
	const n = 60000 // ~12 MB, several scanner refills
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b,
			`{"timestamp":"2025-11-23T11:04:24.000Z","textPayload":"2025-11-23 11:04:24 UTC MARKER%07d LOG: statement %d","severity":"INFO"}`+"\n",
			i, i)
	}
	path := filepath.Join(t.TempDir(), "textpayload.json")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out := make(chan []LogEntry, 64)
	done := make(chan []LogEntry)
	go func() {
		var all []LogEntry
		for batch := range out {
			all = append(all, batch...)
		}
		done <- all
	}()
	p := &JsonParser{}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := p.parseReader(f, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	all := <-done

	if len(all) != n {
		t.Fatalf("got %d entries, want %d", len(all), n)
	}
	for i, e := range all {
		want := fmt.Sprintf("MARKER%07d", i)
		if !strings.Contains(e.Message, want) {
			t.Fatalf("entry %d corrupted: want substring %q, got %.90q", i, want, e.Message)
		}
	}
}
