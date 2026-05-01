package output

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// makeEvents generates n synthetic SessionEvents.
func makeEvents(n int) []analysis.SessionEvent {
	out := make([]analysis.SessionEvent, n)
	t := time.Date(2026, 4, 19, 19, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = analysis.SessionEvent{
			StartTime: t.Add(time.Duration(i) * time.Second),
			EndTime:   t.Add(time.Duration(i+10) * time.Second),
		}
	}
	return out
}

// Old path: build []SessionEventJSON, then json.Marshal.
type sessionEventOldJSON struct {
	Start string `json:"s"`
	End   string `json:"e"`
}

func marshalOld(events []analysis.SessionEvent) ([]byte, error) {
	out := make([]sessionEventOldJSON, 0, len(events))
	for _, se := range events {
		if se.StartTime.IsZero() || se.EndTime.IsZero() {
			continue
		}
		out = append(out, sessionEventOldJSON{
			Start: se.StartTime.Format("2006-01-02T15:04:05"),
			End:   se.EndTime.Format("2006-01-02T15:04:05"),
		})
	}
	return json.Marshal(out)
}

// POC v2 path: lazy wrapper using AppendFormat into flat []byte.
func marshalLazy(events []analysis.SessionEvent) ([]byte, error) {
	if len(events) == 0 {
		return []byte("null"), nil
	}
	buf := make([]byte, 0, len(events)*52+2)
	buf = append(buf, '[')
	first := true
	for _, se := range events {
		if se.StartTime.IsZero() || se.EndTime.IsZero() {
			continue
		}
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = append(buf, `{"s":"`...)
		buf = se.StartTime.AppendFormat(buf, "2006-01-02T15:04:05")
		buf = append(buf, `","e":"`...)
		buf = se.EndTime.AppendFormat(buf, "2006-01-02T15:04:05")
		buf = append(buf, `"}`...)
	}
	buf = append(buf, ']')
	return buf, nil
}

func BenchmarkSessionEventsOld_100k(b *testing.B) {
	events := makeEvents(100_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = marshalOld(events)
	}
}

func BenchmarkSessionEventsLazy_100k(b *testing.B) {
	events := makeEvents(100_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = marshalLazy(events)
	}
}

func BenchmarkSessionEventsOld_1M(b *testing.B) {
	events := makeEvents(1_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = marshalOld(events)
	}
}

func BenchmarkSessionEventsLazy_1M(b *testing.B) {
	events := makeEvents(1_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = marshalLazy(events)
	}
}
