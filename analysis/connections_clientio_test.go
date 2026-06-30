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
	if got := m.ClientIOByCategory["broken pipe (send)"]; got != 2 {
		t.Errorf("broken pipe (send) = %d, want 2", got)
	}
	if got := m.ClientIOByCategory["connection reset by peer (recv)"]; got != 1 {
		t.Errorf("connection reset by peer (recv) = %d, want 1", got)
	}
	if got := m.ClientIOByCategory["connection timed out (recv)"]; got != 1 {
		t.Errorf("connection timed out (recv) = %d, want 1", got)
	}
	if got := m.ClientIOByDatabase["appdb"]; got != 3 {
		t.Errorf("appdb failures = %d, want 3", got)
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
