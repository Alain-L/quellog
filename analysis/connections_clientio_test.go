package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// runClientIO feeds messages through one ConnectionAnalyzer (a full-stream
// consumer — no sharding) and returns the finalized metrics.
func runClientIO(msgs []string) ConnectionMetrics {
	a := NewConnectionAnalyzer()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, m := range msgs {
		e := parser.NewLogEntry(ts.Add(time.Duration(i)*time.Second), m, false)
		a.Process(&e)
	}
	return a.Finalize()
}

func TestClientIOFailureDetection(t *testing.T) {
	m := runClientIO([]string{
		`[100] db=appdb,user=u,app=x,client=1.2.3.4 LOG:  could not send data to client: Broken pipe`,
		`[101] db=appdb,user=u,app=x,client=1.2.3.4 LOG:  could not receive data from client: Connection reset by peer`,
		`[102] db=other,user=u,app=x,client=1.2.3.4 LOG:  could not receive data from client: Connection timed out`,
		// Inline SQLSTATE before the message must be skipped by the anchor.
		`[103] db=appdb,user=u,app=x,client=1.2.3.4 LOG:  08006: could not send data to client: Broken pipe`,
	})

	if m.ClientIOFailureCount != 4 {
		t.Fatalf("ClientIOFailureCount = %d, want 4", m.ClientIOFailureCount)
	}
	if got := m.ClientIOSend["broken pipe"]["appdb"]; got != 2 {
		t.Errorf("send broken pipe on appdb = %d, want 2", got)
	}
	if got := m.ClientIORecv["connection reset by peer"]["appdb"]; got != 1 {
		t.Errorf("recv connection reset by peer on appdb = %d, want 1", got)
	}
	if got := m.ClientIORecv["connection timed out"]["other"]; got != 1 {
		t.Errorf("recv connection timed out on other = %d, want 1", got)
	}
}

// TestClientIOFailureRejectsSQLText locks the false-positive fix: a query
// whose text contains the error string must not be counted.
func TestClientIOFailureRejectsSQLText(t *testing.T) {
	m := runClientIO([]string{
		`[100] db=appdb,user=u,app=x,client=1.2.3.4 LOG:  duration: 2.0 ms  statement: SELECT 'could not send data to client: oops'`,
		`[101] db=appdb,user=u,app=x,client=1.2.3.4 LOG:  statement: SELECT 'could not receive data from client: x'`,
	})
	if m.ClientIOFailureCount != 0 {
		t.Errorf("SQL text must not count as a client I/O failure, got %d", m.ClientIOFailureCount)
	}
}

// TestClientIOFailureNormalizesCursorPosition locks the reason normalization:
// PostgreSQL's "... at character N" cursor-position decoration must not
// fragment one category into per-position variants.
func TestClientIOFailureNormalizesCursorPosition(t *testing.T) {
	m := runClientIO([]string{
		`[100] db=app,user=u,app=x,client=1.2.3.4 LOG:  could not send data to client: Connection timed out at character 13`,
		`[101] db=app,user=u,app=x,client=1.2.3.4 LOG:  could not send data to client: Connection timed out at character 25`,
		`[102] db=app,user=u,app=x,client=1.2.3.4 LOG:  could not send data to client: Connection timed out`,
	})
	if m.ClientIOFailureCount != 3 {
		t.Fatalf("ClientIOFailureCount = %d, want 3", m.ClientIOFailureCount)
	}
	if len(m.ClientIOSend) != 1 {
		t.Errorf("expected a single send category, got %d: %v", len(m.ClientIOSend), m.ClientIOSend)
	}
	if got := m.ClientIOSend["connection timed out"]["app"]; got != 3 {
		t.Errorf("send connection timed out on app = %d, want 3 (cursor position not stripped?)", got)
	}
}

// TestClientIOFailureStripsSQLState locks the reason cleanup: the csv/json
// parsers fold the SQLSTATE back into the message, and it must not leak into
// the category key.
func TestClientIOFailureStripsSQLState(t *testing.T) {
	m := runClientIO([]string{
		`[100] db=app,user=u,app=x,client=1.2.3.4 LOG:  could not receive data from client: Connection reset by peer SQLSTATE = '08006'`,
	})
	if got := m.ClientIORecv["connection reset by peer"]["app"]; got != 1 {
		t.Errorf("reason not cleaned of SQLSTATE: %v", m.ClientIORecv)
	}
}

// TestClientIOFailureConnectionInPrefix locks the false-negative fix: a
// lowercase "connection" in the prefix must not route the I/O error away
// from detection.
func TestClientIOFailureConnectionInPrefix(t *testing.T) {
	m := runClientIO([]string{
		`[100] db=appdb,user=u,app=connection-pool,client=1.2.3.4 LOG:  could not send data to client: Broken pipe`,
	})
	if m.ClientIOFailureCount != 1 {
		t.Errorf("client I/O failure with 'connection' in prefix not detected, got %d", m.ClientIOFailureCount)
	}
}
