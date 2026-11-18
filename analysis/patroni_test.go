package analysis

import (
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// patroniRun feeds "HH:MM:SS LEVEL: message" lines, one slice per member.
func patroniRun(members ...[]string) PatroniMetrics {
	var logs []*PatroniLog
	for _, lines := range members {
		l := NewPatroniLog()
		for _, line := range lines {
			hms, msg, _ := strings.Cut(line, " ")
			ts, _ := time.Parse("15:04:05", hms)
			l.Process(parser.LogEntry{Timestamp: ts, Message: msg})
		}
		logs = append(logs, l)
	}
	return AggregatePatroni(logs)
}

// A replica still following the dead leader after the lock moved must not
// hand the lock back; the old leader's term ends at its own last word.
func TestPatroniFailoverStaleView(t *testing.T) {
	m := patroniRun(
		[]string{"10:00:00 INFO: no action. I am (pg1), the leader with the lock"},
		[]string{
			"10:00:05 INFO: no action. I am (pg2), a secondary, and following a leader (pg1)",
			"10:00:25 INFO: no action. I am (pg2), a secondary, and following a leader (pg1)",
			"10:00:31 INFO: promoted self to leader by acquiring session lock",
			"10:00:31 INFO: no action. I am (pg2), the leader with the lock",
		},
		[]string{
			"10:00:33 INFO: no action. I am (pg3), a secondary, and following a leader (pg1)",
			// long after the change, but pg1's own log says nothing of it
			"10:01:40 INFO: no action. I am (pg3), a secondary, and following a leader (pg1)",
		},
	)
	if len(m.Changes) != 1 || m.Changes[0].Kind != "failover" || m.Changes[0].To != "pg2" {
		t.Fatalf("changes = %+v, want one failover to pg2", m.Changes)
	}
	if got := m.Changes[0].Leaderless; got != 31*time.Second {
		t.Errorf("leaderless = %s, want 31s (from pg1's own last word, not pg2's stale view)", got)
	}
}

// With one member's log only, that member following another leader well
// after the lock moved is a real change, not a stale view. With the former
// holder's own log given, only its own claim counts, however late.
func TestPatroniSingleMemberReturn(t *testing.T) {
	m := patroniRun([]string{
		"10:00:00 INFO: no action. I am (pg1), a secondary, and following a leader (pg2)",
		"10:01:00 INFO: no action. I am (pg1), the leader with the lock",
		"10:03:00 INFO: no action. I am (pg1), a secondary, and following a leader (pg2)",
	})
	if len(m.Changes) != 2 || m.Changes[1].To != "pg2" || m.LeaderlessAtEnd {
		t.Fatalf("changes = %+v, want pg2 -> pg1 -> pg2", m.Changes)
	}
	if len(m.MissingLogs) != 1 || m.MissingLogs[0] != "pg2" {
		t.Errorf("missing logs = %v, want [pg2]", m.MissingLogs)
	}
	// A replica that never led is missing too once a peer names it.
	m = patroniRun([]string{
		"10:00:00 INFO: no action. I am (pg1), the leader with the lock",
		`10:00:05 INFO: Got response from pg3 http://pg3:8008/patroni: {"role": "replica"}`,
	})
	if len(m.MissingLogs) != 1 || m.MissingLogs[0] != "pg3" {
		t.Errorf("missing logs = %v, want [pg3]", m.MissingLogs)
	}
}

// A replica that is neither side of a switchover says so; its log alone
// must not turn the switchover into a failover.
func TestPatroniWitnessSwitchover(t *testing.T) {
	m := patroniRun([]string{
		"10:00:00 INFO: no action. I am (pg3), a secondary, and following a leader (pg1)",
		"10:00:12 INFO: switchover: to pg2, i am pg3",
		"10:00:12 INFO: no action. I am (pg3), a secondary, and following a leader (pg2)",
	})
	if len(m.Changes) != 1 || m.Changes[0].Kind != "switchover" {
		t.Fatalf("changes = %+v, want one switchover", m.Changes)
	}
}

// A leader announcing its demotion ends its term there, not at its last
// heartbeat before a stalled loop.
func TestPatroniDemoteEndsTerm(t *testing.T) {
	m := patroniRun([]string{
		"10:00:00 INFO: no action. I am (pg1), the leader with the lock",
		"10:00:20 INFO: demoting self because DCS is not accessible and I was a leader",
		"10:00:50 INFO: no action. I am (pg1), the leader with the lock",
	})
	if len(m.Changes) != 1 || m.Changes[0].Leaderless != 30*time.Second {
		t.Fatalf("changes = %+v, want one retaken after 30s", m.Changes)
	}
}

// Each sign that a change was asked for is enough on its own. The new leader
// clears the switchover key just after taking the lock, so after the change.
func TestPatroniSwitchoverKind(t *testing.T) {
	for name, signs := range map[string][2]string{
		"request":              {"10:00:10 INFO: received switchover request with leader=pg1 candidate=pg2 scheduled_at=None", ""},
		"graceful":             {"10:00:10 INFO: Demoting self (graceful)", ""},
		"key cleared":          {"", "10:00:14 INFO: Cleaning up failover key after acquiring leader lock..."},
		"none: failover":       {"", ""},
		"dcs demote: failover": {"10:00:10 INFO: demoting self because DCS is not accessible and I was a leader", ""},
	} {
		old := []string{"10:00:00 INFO: no action. I am (pg1), the leader with the lock"}
		if signs[0] != "" {
			old = append(old, signs[0])
		}
		var new []string
		if signs[1] != "" {
			new = append(new, signs[1])
		}
		m := patroniRun(old, append(new, "10:00:12 INFO: no action. I am (pg2), the leader with the lock"))
		want := "switchover"
		if strings.HasSuffix(name, ": failover") {
			want = "failover"
		}
		if len(m.Changes) != 1 || m.Changes[0].Kind != want {
			t.Errorf("%s: changes = %+v, want one %s", name, m.Changes, want)
		}
	}
}

// Overlapping DCS losses are one incident; every member logs the pause.
func TestPatroniClusterWideEvents(t *testing.T) {
	m := patroniRun(
		[]string{
			"10:00:00 INFO: PAUSE: no action. I am (pg1), the leader with the lock",
			"10:00:10 INFO: no action. I am (pg1), the leader with the lock",
			"10:00:15 INFO: PAUSE: no action. I am (pg1), the leader with the lock",
			"10:00:20 INFO: DCS is not accessible",
			"10:00:50 INFO: no action. I am (pg1), the leader with the lock",
		},
		[]string{
			"10:00:01 INFO: no action. I am (pg2), a secondary, and following a leader (pg1)",
			"10:00:16 INFO: PAUSE: no action. I am (pg2), a secondary, and following a leader (pg1)",
			"10:00:30 INFO: DCS is not accessible",
			"10:01:00 INFO: no action. I am (pg2), a secondary, and following a leader (pg1)",
		},
		[]string{ // a loss starting as the incident ends belongs to it
			"10:00:05 INFO: no action. I am (pg3), a secondary, and following a leader (pg1)",
			"10:01:00 INFO: DCS is not accessible",
			"10:01:10 INFO: no action. I am (pg3), a secondary, and following a leader (pg1)",
		},
	)
	if len(m.DCSIncidents) != 1 || m.DCSIncidents[0].End.Sub(m.DCSIncidents[0].Start) != 50*time.Second {
		t.Errorf("dcs incidents = %+v, want one of 50s", m.DCSIncidents)
	}
	var pauses int
	for _, ev := range m.Events {
		if ev.Kind == "pause" {
			pauses++
		}
	}
	if pauses != 1 {
		t.Errorf("pause events = %d, want 1", pauses)
	}
	if len(m.Changes) != 1 || m.Changes[0].Kind != "retaken" || m.Changes[0].Leaderless != 35*time.Second {
		t.Errorf("leader losing the DCS then taking the lock back: changes %+v; want one retaken after 35s", m.Changes)
	}
}
