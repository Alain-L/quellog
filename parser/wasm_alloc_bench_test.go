package parser

import (
	"os"
	"testing"
)

// drain consumes batches until the channel closes, returning each
// batch to the pool the way the wasm pipeline does. Doing this from
// the benchmark goroutine matches the real producer/consumer split.
func drain(t testing.TB, out <-chan []LogEntry) int {
	count := 0
	for batch := range out {
		count += len(batch)
		PutBatch(batch)
	}
	return count
}

func benchParseFromBytes(b *testing.B, path, format string) {
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("fixture not available: %s", path)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		out := make(chan []LogEntry, 64)
		var perr error
		go func() {
			perr = ParseFromBytesStream(data, format, out)
			close(out)
		}()
		drain(b, out)
		if perr != nil {
			b.Fatal(perr)
		}
	}
}

func BenchmarkJSONParse300MB(b *testing.B) {
	benchParseFromBytes(b, "/Users/alain/DALIBO/dev/projects/quellog/_samples/K_300mb.json", "json")
}

func BenchmarkCSVParse425MB(b *testing.B) {
	benchParseFromBytes(b, "/Users/alain/DALIBO/dev/projects/quellog/_samples/C_425mb.csv", "csv")
}

func BenchmarkStderrParse500MB(b *testing.B) {
	benchParseFromBytes(b, "/Users/alain/DALIBO/dev/projects/quellog/_samples/J_500mb.log", "stderr")
}
