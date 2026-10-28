package parser

import (
	"strings"
	"testing"
)

const patroniSample = `2026-09-24 13:43:45,252 ERROR: Failed to get list of machines from http://etcd1:2379/v3beta: ValueError("x")
2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1
Traceback (most recent call last):
  File "x.py", line 1, in <module>
2026-09-24 13:43:52,779 INFO: no action. I am (pg1), the leader with the lock
`

func TestLooksLikePatroni(t *testing.T) {
	journal := "Sep 24 13:43:50 db1 patroni[812]: 2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1\n" +
		"Sep 24 13:43:52 db1 patroni[812]: 2026-09-24 13:43:52,779 INFO: no action. I am (pg1), the leader with the lock\n"
	stderr := "2026-09-24 13:43:50.360 UTC [36] LOG:  database system is ready to accept connections\n" +
		"2026-09-24 13:43:52.779 UTC [40] LOG:  checkpoint starting: time\n"
	for name, c := range map[string]struct {
		sample string
		want   bool
	}{"text": {patroniSample, true},
		"mostly untimestamped": {"2026-09-24 13:44:36,629 INFO: pg_controldata:\n  Catalog version number: 202406281\n  Database cluster state: shut down\n  pg_control version number: 1700\n2026-09-24 13:44:36,642 INFO: primary_timeline=2\n", true}, "journalctl": {journal, true},
		"json": {`{"asctime": "2026-09-24 14:09:31,727", "levelname": "INFO", "message": "Lock owner: None; I am pg1"}` + "\n" +
			`{"asctime": "2026-09-24 14:09:44,926", "levelname": "INFO", "message": "no action. I am (pg1), the leader with the lock"}` + "\n", true},
		"postgres jsonlog": {`{"timestamp":"2026-09-24 14:09:31.727 UTC","pid":36,"error_severity":"LOG","message":"checkpoint starting: time"}` + "\n" +
			`{"timestamp":"2026-09-24 14:09:44.926 UTC","pid":36,"error_severity":"LOG","message":"checkpoint complete"}` + "\n", false}, "postgres stderr": {stderr, false},
		"one Patroni line alone":                    {"2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1\n", false},
		"postgres syslog with two Patroni lines":    {strings.Repeat("Apr 02 14:55:51.000000 172.28.0.2 postgres[43]: [1-1] 2026-04-02 14:55:51.484 UTC [43] LOG:  checkpoint starting: time\n", 3) + "2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1\n2026-09-24 13:43:52,779 INFO: no action. I am (pg1), the leader with the lock\n", false},
		"postgres stderr quoting two Patroni lines": {stderr + stderr + stderr + "2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1\n2026-09-24 13:43:52,779 INFO: no action. I am (pg1), the leader with the lock\n", false}} {
		if got := looksLikePatroni(c.sample); got != c.want {
			t.Errorf("%s: looksLikePatroni = %v, want %v", name, got, c.want)
		}
	}
}

func TestPatroniParseReader(t *testing.T) {
	out := make(chan []LogEntry, 4)
	if _, err := (&PatroniParser{}).parseReader(strings.NewReader(patroniSample), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var got []LogEntry
	for b := range out {
		got = append(got, b...)
	}
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5 (3 lines + 2 traceback continuations)", len(got))
	}
	if got[1].Message != "INFO: Lock owner: None; I am pg1" || got[1].Timestamp.Format("15:04:05.000") != "13:43:50.360" {
		t.Errorf("entry 1 = %q at %s", got[1].Message, got[1].Timestamp)
	}
	j, ok := parsePatroniJSON(`{"asctime": "2026-09-24 14:09:31,727", "levelname": "WARNING", "message": "Postgresql is not running."}`)
	if !ok || j.Message != "WARNING: Postgresql is not running." || j.Timestamp.Format("15:04:05.000") != "14:09:31.727" {
		t.Errorf("parsePatroniJSON = %+v, %v", j, ok)
	}
	if !got[2].IsContinuation || !got[3].IsContinuation || got[4].IsContinuation {
		t.Errorf("continuation flags wrong: %v %v %v", got[2].IsContinuation, got[3].IsContinuation, got[4].IsContinuation)
	}
}

func TestPatroniParseReaderSkipsOtherLines(t *testing.T) {
	out := make(chan []LogEntry, 4)
	skipped, err := (&PatroniParser{}).parseReader(strings.NewReader(patroniSample+"2026-09-24 13:43:53.000 UTC [36] LOG:  checkpoint starting: time\n"), out)
	if err != nil || skipped != 1 {
		t.Fatalf("skipped = %d, err = %v, want 1 line skipped", skipped, err)
	}
}
