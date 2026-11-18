package output

import (
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// PatroniJSON is the "patroni" key of the JSON report.
type PatroniJSON struct {
	StartDate      string                `json:"start_date"`
	EndDate        string                `json:"end_date"`
	Duration       string                `json:"duration"`
	TotalLogs      int                   `json:"total_logs"`
	ErrorCount     int                   `json:"error_count"`
	WarningCount   int                   `json:"warning_count"`
	Leader         string                `json:"leader,omitempty"`
	LeaderSince    string                `json:"leader_since,omitempty"`
	LeaderChanges  int                   `json:"leader_changes"`
	Switchovers    int                   `json:"switchovers"`
	Failovers      int                   `json:"failovers"`
	LockRetaken    int                   `json:"lock_retaken"`
	LeaderlessTime string                `json:"leaderless_time"`
	DCSIncidents   []PatroniIncidentJSON `json:"dcs_incidents,omitempty"`
	Members        []PatroniMemberJSON   `json:"members"`
	MissingLogs    []string              `json:"members_without_log,omitempty"`
	Leadership     []PatroniTermJSON     `json:"leadership,omitempty"`
	Changes        []PatroniChangeJSON   `json:"changes,omitempty"`
	EventCounts    map[string]int        `json:"event_counts,omitempty"`
	Events         []PatroniEventJSON    `json:"events,omitempty"`
	Messages       []PatroniMessageJSON  `json:"messages,omitempty"`
}

type PatroniIncidentJSON struct {
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Duration string   `json:"duration"`
	Members  []string `json:"members"`
}

type PatroniMemberJSON struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
	LeaderTime string `json:"leader_time"`
	TotalLogs  int    `json:"total_logs"`
}

type PatroniTermJSON struct {
	Member   string `json:"member"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Duration string `json:"duration"`
}

type PatroniChangeJSON struct {
	Timestamp  string                    `json:"timestamp"`
	Kind       string                    `json:"kind"`
	From       string                    `json:"from"`
	To         string                    `json:"to"`
	Leaderless string                    `json:"leaderless"`
	Postgres   []ServerTimelineEventJSON `json:"postgres,omitempty"`
}

type PatroniEventJSON struct {
	Timestamp string `json:"timestamp"`
	Member    string `json:"member"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail,omitempty"`
}

type PatroniMessageJSON struct {
	ID        string `json:"id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
	FirstDate string `json:"first_date,omitempty"`
	LastDate  string `json:"last_date,omitempty"`
}

const patroniTS = "2006-01-02 15:04:05"

func buildPatroniJSON(m *analysis.PatroniMetrics) PatroniJSON {
	ts := func(t time.Time) string { return t.Format(patroniTS) }
	j := PatroniJSON{
		StartDate:      ts(m.Start),
		EndDate:        ts(m.End),
		Duration:       m.End.Sub(m.Start).String(),
		TotalLogs:      m.Entries,
		ErrorCount:     m.Errors,
		WarningCount:   m.Warnings,
		LeaderlessTime: patroniLeaderless(m).String(),
		Members:        []PatroniMemberJSON{},
		MissingLogs:    m.MissingLogs,
	}
	if leader, since, ok := patroniLeaderAtEnd(m); ok {
		j.Leader, j.LeaderSince = leader, ts(since)
	}
	for _, mb := range m.Members {
		j.Members = append(j.Members, PatroniMemberJSON{Name: mb.Name, Role: mb.Role, FirstSeen: ts(mb.First), LastSeen: ts(mb.Last), LeaderTime: mb.LeaderTime.String(), TotalLogs: mb.Entries})
	}
	for _, in := range m.DCSIncidents {
		j.DCSIncidents = append(j.DCSIncidents, PatroniIncidentJSON{Start: ts(in.Start), End: ts(in.End), Duration: in.End.Sub(in.Start).String(), Members: in.Members})
	}
	for _, t := range m.Leadership {
		j.Leadership = append(j.Leadership, PatroniTermJSON{Member: t.Member, Start: ts(t.Start), End: ts(t.End), Duration: t.End.Sub(t.Start).String()})
	}
	for _, c := range m.Changes {
		switch c.Kind {
		case "switchover":
			j.Switchovers++
		case "failover":
			j.Failovers++
		default:
			j.LockRetaken++
		}
		cj := PatroniChangeJSON{Timestamp: ts(c.Timestamp), Kind: c.Kind, From: c.From, To: c.To, Leaderless: c.Leaderless.String()}
		for _, ev := range c.Postgres {
			cj.Postgres = append(cj.Postgres, ServerTimelineEventJSON{Timestamp: ts(ev.Timestamp), Kind: ev.Kind, Detail: ev.Detail})
		}
		j.Changes = append(j.Changes, cj)
	}
	for _, ev := range m.Events {
		if j.EventCounts == nil {
			j.EventCounts = map[string]int{}
		}
		j.EventCounts[ev.Kind]++
		j.Events = append(j.Events, PatroniEventJSON{Timestamp: ts(ev.Timestamp), Member: ev.Member, Kind: ev.Kind, Detail: ev.Detail})
	}
	for _, msg := range m.Messages {
		mj := PatroniMessageJSON{ID: msg.ID, Level: msg.Level, Message: msg.Message, Count: msg.Count}
		if n := len(msg.Times); n > 0 {
			mj.FirstDate, mj.LastDate = ts(msg.Times[0]), ts(msg.Times[n-1])
		}
		j.Messages = append(j.Messages, mj)
	}
	j.LeaderChanges = j.Switchovers + j.Failovers
	return j
}

// patroniLeaderAtEnd returns the lock holder when the logs end, if any:
// the last term must reach the last sighting of the lock.
func patroniLeaderAtEnd(m *analysis.PatroniMetrics) (string, time.Time, bool) {
	if len(m.Leadership) == 0 || m.LeaderlessAtEnd {
		return "", time.Time{}, false
	}
	t := m.Leadership[len(m.Leadership)-1]
	return t.Member, t.Start, true
}

// patroniLeaderless sums the gaps between consecutive lock terms, which is
// the sum of the changes' own leaderless times.
func patroniLeaderless(m *analysis.PatroniMetrics) time.Duration {
	var d time.Duration
	for i := 1; i < len(m.Leadership); i++ {
		d += m.Leadership[i].Start.Sub(m.Leadership[i-1].End)
	}
	return d
}
