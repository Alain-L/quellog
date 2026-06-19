package parser

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSkipBOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"strips leading BOM", append(append([]byte{}, bom...), []byte("hello")...), []byte("hello")},
		{"no BOM passes through", []byte("hello"), []byte("hello")},
		{"partial BOM prefix preserved", []byte{0xEF, 0xBB}, []byte{0xEF, 0xBB}},
		{"empty", []byte{}, []byte{}},
		{"BOM only", bom, []byte{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := io.ReadAll(skipBOM(bytes.NewReader(c.in)))
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if !bytes.Equal(got, c.want) {
				t.Errorf("skipBOM(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestParseFileBOM locks the monkey-test finding: a leading UTF-8 BOM must not
// change the parsed entry count for any format. Before the fix a BOM silently
// dropped the first stderr record and made CSV/JSON fail detection outright.
func TestParseFileBOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	fixtures := []string{
		"../test/testdata/stderr.log",
		"../test/testdata/csvlog.csv",
		"../test/testdata/jsonlog.json",
	}
	for _, src := range fixtures {
		src := src
		t.Run(filepath.Base(src), func(t *testing.T) {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			base := count(t, write(t, filepath.Base(src), raw))
			withBOM := count(t, write(t, "bom_"+filepath.Base(src), append(append([]byte{}, bom...), raw...)))
			if base == 0 {
				t.Fatalf("baseline parsed 0 entries from %s", src)
			}
			if withBOM != base {
				t.Errorf("%s: BOM changed entry count: %d with BOM vs %d without", src, withBOM, base)
			}
		})
	}
}

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func count(t *testing.T, filename string) int {
	t.Helper()
	out := make(chan []LogEntry, 64)
	done := make(chan int, 1)
	go func() {
		n := 0
		for batch := range out {
			for _, e := range batch {
				if !e.IsContinuation {
					n++
				}
			}
		}
		done <- n
	}()
	if err := ParseFile(filename, out); err != nil {
		t.Fatalf("ParseFile(%s): %v", filename, err)
	}
	close(out)
	return <-done
}
