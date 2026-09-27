package output

import (
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// PgBouncerJSON is the JSON view of one pooler, the entry type of the
// "pgbouncer" list. Times are ISO strings on the log's own clock
// ("YYYY-MM-DDTHH:MM:SS"), as in the other sections.
type PgBouncerJSON struct {
	Name    string   `json:"name"`
	Files   []string `json:"files"`
	Listen  []string `json:"listen,omitempty"`
	Version string   `json:"version,omitempty"`

	MinTimestamp string               `json:"min_timestamp"`
	MaxTimestamp string               `json:"max_timestamp"`
	Events       []PgBouncerEventJSON `json:"events,omitempty"`

	Lines             int `json:"lines"`
	LinesRead         int `json:"lines_read"`
	LinesUnrecognized int `json:"lines_unrecognized"`
	DebugLines        int `json:"debug_lines"`

	StatsSampleCount  int                  `json:"stats_sample_count"`
	AvgXactsPerSec    float64              `json:"avg_xacts_per_sec"`
	MaxXactsPerSec    int                  `json:"max_xacts_per_sec"`
	AvgQueriesPerSec  float64              `json:"avg_queries_per_sec"`
	MaxQueriesPerSec  int                  `json:"max_queries_per_sec"`
	AvgInBytesPerSec  float64              `json:"avg_in_bytes_per_sec"`
	MaxInBytesPerSec  int                  `json:"max_in_bytes_per_sec"`
	AvgOutBytesPerSec float64              `json:"avg_out_bytes_per_sec"`
	MaxOutBytesPerSec int                  `json:"max_out_bytes_per_sec"`
	AvgXactMicros     float64              `json:"avg_xact_us"`
	MaxXactMicros     int64                `json:"max_xact_us"`
	AvgQueryMicros    float64              `json:"avg_query_us"`
	MaxQueryMicros    int64                `json:"max_query_us"`
	AvgWaitMicros     float64              `json:"avg_wait_us"`
	MaxWaitMicros     int64                `json:"max_wait_us"`
	StatsSeries       []PgBouncerStatsJSON `json:"stats_series,omitempty"`

	MaxXactsTimestamp   string `json:"max_xacts_per_sec_timestamp,omitempty"`
	MaxQueriesTimestamp string `json:"max_queries_per_sec_timestamp,omitempty"`
	MaxInTimestamp      string `json:"max_in_bytes_per_sec_timestamp,omitempty"`
	MaxOutTimestamp     string `json:"max_out_bytes_per_sec_timestamp,omitempty"`
	MaxXactTimestamp    string `json:"max_xact_us_timestamp,omitempty"`
	MaxQueryTimestamp   string `json:"max_query_us_timestamp,omitempty"`
	MaxWaitTimestamp    string `json:"max_wait_us_timestamp,omitempty"`

	WaitEpisodes []PgBouncerWaitEpisodeJSON `json:"wait_episodes,omitempty"`

	LoginAttempts       int                        `json:"login_attempts"`
	ServerConnections   int                        `json:"server_connections"`
	Sessions            int                        `json:"sessions"`
	TotalSessionSeconds int64                      `json:"total_session_seconds"`
	SessionDistribution map[string]int             `json:"session_distribution,omitempty"`
	SessionReasons      []PgBouncerCloseReasonJSON `json:"session_reasons,omitempty"`

	ConnectionsPerDatabase map[string]int `json:"connections_per_database,omitempty"`
	ConnectionsPerUser     map[string]int `json:"connections_per_user,omitempty"`
	ConnectionsPerHost     map[string]int `json:"connections_per_host,omitempty"`
	SessionsPerDatabase    map[string]int `json:"sessions_per_database,omitempty"`
	SessionsPerUser        map[string]int `json:"sessions_per_user,omitempty"`
	SessionsPerHost        map[string]int `json:"sessions_per_host,omitempty"`

	ReservePoolActivations int                  `json:"reserve_pool_activations"`
	Errors                 []PgBouncerErrorJSON `json:"errors,omitempty"`
	TotalErrors            int                  `json:"total_errors"`
}

// PgBouncerStatsJSON is one "stats:" snapshot.
type PgBouncerStatsJSON struct {
	Time        string `json:"time"`
	Xacts       int    `json:"xacts_per_sec"`
	Queries     int    `json:"queries_per_sec"`
	InBytes     int    `json:"in_bytes_per_sec"`
	OutBytes    int    `json:"out_bytes_per_sec"`
	XactMicros  int64  `json:"xact_us"`
	QueryMicros int64  `json:"query_us"`
	WaitMicros  int64  `json:"wait_us"`
}

// PgBouncerEventJSON is one step of the pooler's timeline.
type PgBouncerEventJSON struct {
	Timestamp string `json:"timestamp"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail,omitempty"`
}

// PgBouncerWaitEpisodeJSON is one stretch during which clients queued.
type PgBouncerWaitEpisodeJSON struct {
	Start         string `json:"start"`
	End           string `json:"end"`
	PeakWaitUs    int64  `json:"peak_wait_us"`
	PeakTimestamp string `json:"peak_timestamp"`
}

// PgBouncerErrorJSON is one normalized warning or error.
type PgBouncerErrorJSON struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
	First   string `json:"first"`
	Last    string `json:"last"`
}

// PgBouncerCloseReasonJSON counts the sessions closed for one reason.
type PgBouncerCloseReasonJSON struct {
	Reason       string `json:"reason"`
	Count        int    `json:"count"`
	TotalSeconds int64  `json:"total_seconds"`
}

// isoTime renders a time on the log's clock, or "" for a zero time.
func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02T15:04:05")
}

// pgbouncerJSON builds the JSON view of the poolers.
func pgbouncerJSON(poolers []analysis.PgBouncerMetrics) []PgBouncerJSON {
	out := make([]PgBouncerJSON, len(poolers))
	for i, m := range poolers {
		j := PgBouncerJSON{
			Name: m.Name, Files: m.Files, Listen: m.Listen, Version: m.Version,
			MinTimestamp: isoTime(m.MinTimestamp), MaxTimestamp: isoTime(m.MaxTimestamp),
			Lines: m.Lines, LinesRead: m.LinesRead, LinesUnrecognized: m.LinesUnrecognized, DebugLines: m.DebugLines,
			StatsSampleCount: m.StatsSampleCount,
			AvgXactsPerSec:   m.AvgXactsPerSec, MaxXactsPerSec: m.MaxXactsPerSec,
			AvgQueriesPerSec: m.AvgQueriesPerSec, MaxQueriesPerSec: m.MaxQueriesPerSec,
			AvgInBytesPerSec: m.AvgInBytesPerSec, MaxInBytesPerSec: m.MaxInBytesPerSec,
			AvgOutBytesPerSec: m.AvgOutBytesPerSec, MaxOutBytesPerSec: m.MaxOutBytesPerSec,
			AvgXactMicros: m.AvgXactMicros, MaxXactMicros: m.MaxXactMicros,
			AvgQueryMicros: m.AvgQueryMicros, MaxQueryMicros: m.MaxQueryMicros,
			AvgWaitMicros: m.AvgWaitMicros, MaxWaitMicros: m.MaxWaitMicros,
			MaxXactsTimestamp:   isoTime(m.MaxXactsTimestamp),
			MaxQueriesTimestamp: isoTime(m.MaxQueriesTimestamp),
			MaxInTimestamp:      isoTime(m.MaxInTimestamp),
			MaxOutTimestamp:     isoTime(m.MaxOutTimestamp),
			MaxXactTimestamp:    isoTime(m.MaxXactTimestamp),
			MaxQueryTimestamp:   isoTime(m.MaxQueryTimestamp),
			MaxWaitTimestamp:    isoTime(m.MaxWaitTimestamp),
			LoginAttempts:       m.LoginAttempts, ServerConnections: m.ServerConnections,
			Sessions: m.Sessions, TotalSessionSeconds: m.TotalSessionSeconds,
			SessionDistribution:    m.SessionDistribution,
			ConnectionsPerDatabase: m.ConnectionsPerDatabase, ConnectionsPerUser: m.ConnectionsPerUser,
			ConnectionsPerHost: m.ConnectionsPerHost, SessionsPerDatabase: m.SessionsPerDatabase,
			SessionsPerUser: m.SessionsPerUser, SessionsPerHost: m.SessionsPerHost,
			ReservePoolActivations: m.ReservePoolActivations, TotalErrors: m.TotalErrors,
		}
		for _, e := range m.Events {
			j.Events = append(j.Events, PgBouncerEventJSON{isoTime(e.Time), e.Kind, e.Detail})
		}
		for _, p := range m.StatsSeries {
			j.StatsSeries = append(j.StatsSeries, PgBouncerStatsJSON{isoTime(p.Time), p.Xacts, p.Queries,
				p.InBytes, p.OutBytes, p.XactMicros, p.QueryMicros, p.WaitMicros})
		}
		for _, e := range m.WaitEpisodes {
			j.WaitEpisodes = append(j.WaitEpisodes, PgBouncerWaitEpisodeJSON{isoTime(e.Start), isoTime(e.End), e.PeakUs, isoTime(e.PeakTime)})
		}
		for _, r := range m.SessionReasons {
			j.SessionReasons = append(j.SessionReasons, PgBouncerCloseReasonJSON{r.Reason, r.Count, r.TotalSeconds})
		}
		for _, e := range m.Errors {
			j.Errors = append(j.Errors, PgBouncerErrorJSON{e.Message, e.Count, isoTime(e.First), isoTime(e.Last)})
		}
		out[i] = j
	}
	return out
}
