package analysis

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// PatroniMetrics is the cluster view rebuilt from one Patroni log per member.
type PatroniMetrics struct {
	Entries, Errors, Warnings int
	Start, End                time.Time
	Members                   []PatroniMember
	Leadership                []PatroniTerm   // who held the leader lock, in order
	Changes                   []PatroniChange // leader lock moving to another member
	Events                    []PatroniEvent
	Messages                  []PatroniMessage // warnings and errors, grouped
	LeaderlessAtEnd           bool             // the last sighting says nobody holds the lock
	MissingLogs               []string         // members named in the logs whose own log was not given
	DCSIncidents              []PatroniIncident
}

// PatroniIncident is a span during which at least one member could not
// reach the DCS; overlapping member losses make one incident.
type PatroniIncident struct {
	Start, End time.Time
	Members    []string
}

type PatroniMember struct {
	Name        string
	First, Last time.Time
	Role        string // last role the member reported: leader, replica or unknown
	LeaderTime  time.Duration
	Entries     int
}

type PatroniTerm struct {
	Member     string
	Start, End time.Time
}

type PatroniChange struct {
	Timestamp  time.Time
	From, To   string
	Kind       string        // switchover (requested), failover, or retaken (same member)
	Leaderless time.Duration // last sight of the old leader to first sight of the new one
	Postgres   []ServerTimelineEvent
}

type PatroniEvent struct {
	Timestamp            time.Time
	Member, Kind, Detail string
}

// PatroniMessage is a warning or an error, grouped by level and message
// with its values masked. ID is its --event-detail handle; Times dates
// each occurrence, in time order; Members are the logs that wrote it;
// Example is the first occurrence as written.
type PatroniMessage struct {
	ID, Level, Message, Example string
	Count                       int
	Members                     []string
	Times                       []time.Time
}

// patroniMsg is one member's occurrences of a message.
type patroniMsg struct {
	times   []time.Time
	example string
}

// PatroniLog accumulates one member's log; AggregatePatroni merges them.
type PatroniLog struct {
	member                    string
	entries, errors, warnings int
	first, last               time.Time
	role                      string
	paused, seenHeartbeat     bool
	dcsDownSince              time.Time // zero while the DCS answers
	sightings                 []leaderSighting
	peers                     []string // members this one contacted over the REST API
	events                    []PatroniEvent
	messages                  map[[2]string]*patroniMsg
}

// leaderSighting is one statement of who holds the lock ("" = nobody).
// self marks the holder speaking for itself; with no leader, the holder
// giving the lock up.
type leaderSighting struct {
	t      time.Time
	leader string
	self   bool
}

var (
	patroniHeartbeatRe = regexp.MustCompile(`I am \(([^)]+)\), (?:(the leader with the lock)|a secondary, and following a leader \(([^)]+)\))`)
	patroniOwnerRe     = regexp.MustCompile(`^Lock owner: (\S+); I am (\S+)$`)
	patroniPeerRe      = regexp.MustCompile(`^(?:Got response from|Request failed to) ([^\s:]+)`)
	pyExceptionRe      = regexp.MustCompile(`(\w+(?:Error|Exception|Failed|Timeout))\(.*$`)
)

// patroniEventKinds maps a message prefix to the event it records; an
// empty kind drops a line that repeats the previous one. First match wins.
var patroniEventKinds = []struct{ prefix, kind string }{
	{"initialized a new cluster", "bootstrap"},
	{"replica has been created using", "replica_created"},
	{"received switchover request", "switchover_request"},
	{"switchover: to ", "switchover_seen"},
	{"demoting self because DCS", "demote"},
	{"demoted self because DCS", ""},
	{"Demoting self (offline)", ""},
	{"Demoting self", "demote"},
	{"promoted self to leader", "promote"},
	{"Cleaning up failover key", "failover_key"},
	{"pg_rewind exit code=0", "rewind_done"},
	{"pg_rewind exit code=", ""},
	{"running pg_rewind from dbname=", ""},
	{"running pg_rewind from", "rewind"},
	{"Failed to rewind", "rewind_failed"},
	{"Removing data directory", "reinit"},
	{"doing crash recovery", "crash_recovery"},
	{"My wal position exceeds maximum replication lag", "lag_refused"},
	{"Assigning synchronous standby status to", "sync_standby"},
}

func NewPatroniLog() *PatroniLog {
	return &PatroniLog{role: "unknown", messages: map[[2]string]*patroniMsg{}}
}

func (l *PatroniLog) Process(e parser.LogEntry) {
	// A FromPgBackRest entry is the parser's marker of a source, or a
	// pgBackRest line: not a Patroni one.
	if e.IsContinuation || e.FromPgBackRest {
		return
	}
	l.entries++
	if l.first.IsZero() {
		l.first = e.Timestamp
	}
	l.last = e.Timestamp
	level, msg, _ := strings.Cut(e.Message, ": ")
	switch level {
	case "WARNING":
		l.warnings++
		l.message(level, msg, e.Timestamp)
	case "ERROR", "CRITICAL":
		l.errors++
		l.message(level, msg, e.Timestamp)
	}
	if msg == "DCS is not accessible" {
		if l.dcsDownSince.IsZero() {
			l.dcsDownSince = e.Timestamp
			l.event(e.Timestamp, "dcs_lost", msg)
		}
		// Without the DCS a leader stops acting as primary (failsafe off).
		if l.role == "leader" {
			l.role = "unknown"
			l.sightings = append(l.sightings, leaderSighting{e.Timestamp, "", false})
		}
		return
	}
	rest, paused := strings.CutPrefix(msg, "PAUSE: ")
	heartbeat := patroniHeartbeatRe.FindStringSubmatch(rest)
	owner := patroniOwnerRe.FindStringSubmatch(msg)
	if (heartbeat != nil || owner != nil) && !l.dcsDownSince.IsZero() {
		l.event(e.Timestamp, "dcs_restored", "after "+e.Timestamp.Sub(l.dcsDownSince).Round(time.Second).String())
		l.dcsDownSince = time.Time{}
	}
	if m := heartbeat; m != nil {
		l.member = m[1]
		if m[2] != "" {
			l.role = "leader"
			l.sightings = append(l.sightings, leaderSighting{e.Timestamp, m[1], true})
		} else {
			l.role = "replica"
			l.sightings = append(l.sightings, leaderSighting{e.Timestamp, m[3], false})
		}
		if l.seenHeartbeat && paused != l.paused {
			kind := "resume"
			if paused {
				kind = "pause"
			}
			l.event(e.Timestamp, kind, "")
		}
		l.paused, l.seenHeartbeat = paused, true
		return
	}
	if m := owner; m != nil {
		l.member = m[2]
		owner := m[1]
		if owner == "None" {
			owner = ""
		}
		l.sightings = append(l.sightings, leaderSighting{e.Timestamp, owner, owner == m[2]})
		return
	}
	if m := patroniPeerRe.FindStringSubmatch(msg); m != nil {
		l.peers = append(l.peers, m[1])
		return
	}
	for _, k := range patroniEventKinds {
		if strings.HasPrefix(msg, k.prefix) {
			if k.kind != "" {
				l.event(e.Timestamp, k.kind, msg)
			}
			if k.kind == "demote" && l.role == "leader" {
				l.role = "unknown"
				l.sightings = append(l.sightings, leaderSighting{e.Timestamp, "", true})
			}
			return
		}
	}
}

// message counts one warning or error of the log.
func (l *PatroniLog) message(level, msg string, ts time.Time) {
	k := [2]string{level, patroniNormalize(msg)}
	pm := l.messages[k]
	if pm == nil {
		pm = &patroniMsg{example: msg}
		l.messages[k] = pm
	}
	pm.times = append(pm.times, ts)
}

// patroniNormalize groups messages that differ only by values; a Python
// exception keeps its name, not its repr.
func patroniNormalize(msg string) string {
	return NormalizeEvent(pyExceptionRe.ReplaceAllString(msg, "$1"))
}

func (l *PatroniLog) event(t time.Time, kind, detail string) {
	l.events = append(l.events, PatroniEvent{Timestamp: t, Kind: kind, Detail: detail})
}

const (
	// switchoverWindow bounds how long before a lock change a switchover
	// request or a graceful demote still explains it.
	switchoverWindow = time.Minute
	// staleView bounds how long a replica may keep naming the former leader
	// after the lock moved: one loop, well under the default ttl of 30 s.
	staleView = 30 * time.Second
	// pgBefore and pgAfter frame the PostgreSQL events kept for a change:
	// the old primary stops just before its last sighting, the new one and
	// the restarted replicas settle within a loop or two after the change.
	pgBefore = 10 * time.Second
	pgAfter  = 30 * time.Second
)

// PatroniMessageID is the --event-detail handle of a Patroni warning or
// error: the PostgreSQL events' prefixes (er-, wa-; fa- for CRITICAL) and
// hashing (GenerateEventID), on a key that names the source.
func PatroniMessageID(level, message string) string {
	sev := level
	if level == "CRITICAL" {
		sev = "FATAL"
	}
	return GenerateEventID(sev, "patroni|"+level+"|"+message)
}

// AnalyzePatroniMembers reads one member's log per feed, each feed writing
// its entries to the channel it is given and closing it, keeps the entries
// within window's time bounds, and merges the members. Only the time bounds
// apply: the attribute filters select PostgreSQL sessions, which Patroni
// lines do not describe. It returns nil when no entry was read.
func AnalyzePatroniMembers(ctx context.Context, feeds []func(chan<- []parser.LogEntry), window parser.LogFilters) *PatroniMetrics {
	window = parser.LogFilters{BeginT: window.BeginT, EndT: window.EndT}
	var logs []*PatroniLog
	for _, feed := range feeds {
		// One member at a time: each is one point of view.
		raw := make(chan []parser.LogEntry, 64)
		go feed(raw)
		var in <-chan []parser.LogEntry = raw
		if !window.IsEmpty() {
			filtered := make(chan []parser.LogEntry, 64)
			go parser.FilterStream(ctx, raw, filtered, window)
			in = filtered
		}
		l := NewPatroniLog()
		for batch := range in {
			for _, e := range batch {
				l.Process(e)
			}
		}
		logs = append(logs, l)
	}
	m := AggregatePatroni(logs)
	if m.Entries == 0 {
		return nil
	}
	return &m
}

func AggregatePatroni(logs []*PatroniLog) PatroniMetrics {
	var m PatroniMetrics
	var sightings []leaderSighting
	var named []string // members named by others: REST peers here, leaders below
	msgs := map[[2]string]*PatroniMessage{}
	for _, l := range logs {
		if l.entries == 0 {
			continue
		}
		name := l.member
		if name == "" {
			name = "unknown"
		}
		m.Entries += l.entries
		m.Errors += l.errors
		m.Warnings += l.warnings
		if m.Start.IsZero() || l.first.Before(m.Start) {
			m.Start = l.first
		}
		if l.last.After(m.End) {
			m.End = l.last
		}
		m.Members = append(m.Members, PatroniMember{Name: name, First: l.first, Last: l.last, Role: l.role, Entries: l.entries})
		for _, ev := range l.events {
			ev.Member = name
			m.Events = append(m.Events, ev)
		}
		for k, pm := range l.messages {
			agg := msgs[k]
			if agg == nil {
				agg = &PatroniMessage{Level: k[0], Message: k[1], Example: pm.example}
				msgs[k] = agg
			} else if len(pm.times) > 0 && (len(agg.Times) == 0 || pm.times[0].Before(agg.Times[0])) {
				agg.Example = pm.example // the first occurrence as written
			}
			agg.Count += len(pm.times)
			agg.Times = append(agg.Times, pm.times...)
			agg.Members = append(agg.Members, name)
		}
		sightings = append(sightings, l.sightings...)
		named = append(named, l.peers...)
	}
	sort.SliceStable(sightings, func(i, j int) bool { return sightings[i].t.Before(sightings[j].t) })
	sort.SliceStable(m.Events, func(i, j int) bool { return m.Events[i].Timestamp.Before(m.Events[j].Timestamp) })
	m.Events = dedupPause(m.Events)
	m.DCSIncidents = dcsIncidents(m.Events, m.End)
	sort.Slice(m.Members, func(i, j int) bool { return m.Members[i].Name < m.Members[j].Name })
	m.buildLeadership(sightings)
	seen := map[string]bool{}
	for _, mb := range m.Members {
		seen[mb.Name] = true
	}
	for _, t := range m.Leadership {
		named = append(named, t.Member)
	}
	for _, name := range named {
		if !seen[name] {
			seen[name] = true
			m.MissingLogs = append(m.MissingLogs, name)
		}
	}
	sort.Strings(m.MissingLogs)
	for _, msg := range msgs {
		slices.SortFunc(msg.Times, time.Time.Compare)
		sort.Strings(msg.Members)
		msg.ID = PatroniMessageID(msg.Level, msg.Message)
		m.Messages = append(m.Messages, *msg)
	}
	sort.Slice(m.Messages, func(i, j int) bool {
		if m.Messages[i].Count != m.Messages[j].Count {
			return m.Messages[i].Count > m.Messages[j].Count
		}
		return m.Messages[i].Message < m.Messages[j].Message
	})
	return m
}

// buildLeadership turns the merged sightings into lock terms and changes.
// A new holder is taken at its first sighting. A replica naming a former
// holder may be one loop behind: the return needs that member to claim the
// lock itself when its log was given, and, when it was not, to come well
// after the lock last moved.
func (m *PatroniMetrics) buildLeadership(sightings []leaderSighting) {
	var cur *PatroniTerm
	var lastAny, lastSelf, moved time.Time
	logged := map[string]bool{}
	for _, mb := range m.Members {
		logged[mb.Name] = true
	}
	held := map[string]bool{}
	// A term ends at the holder's own last word when it has one: replicas
	// keep reporting a dead leader until its lock expires.
	end := func() time.Time {
		if !lastSelf.IsZero() {
			return lastSelf
		}
		return lastAny
	}
	for _, s := range sightings {
		switch {
		case s.leader == "":
			if cur != nil {
				cur.End = end()
				if s.self {
					cur.End = s.t
				}
				cur, moved = nil, s.t
			}
		case cur != nil && s.leader == cur.Member:
			lastAny = s.t
			if s.self {
				lastSelf = s.t
			}
		case held[s.leader] && !s.self && (logged[s.leader] || s.t.Sub(moved) < staleView):
			// stale view of a former holder
		default:
			if cur != nil {
				cur.End = end()
			}
			if n := len(m.Leadership); n > 0 {
				last := m.Leadership[n-1]
				m.Changes = append(m.Changes, PatroniChange{Timestamp: s.t, From: last.Member, To: s.leader, Leaderless: s.t.Sub(last.End)})
			}
			m.Leadership = append(m.Leadership, PatroniTerm{Member: s.leader, Start: s.t})
			cur, lastAny, lastSelf, moved = &m.Leadership[len(m.Leadership)-1], s.t, time.Time{}, s.t
			if s.self {
				lastSelf = s.t
			}
			held[s.leader] = true
		}
	}
	if cur != nil {
		cur.End = end()
	}
	m.LeaderlessAtEnd = cur == nil && len(m.Leadership) > 0
	for i := range m.Leadership {
		t := m.Leadership[i]
		for j := range m.Members {
			if m.Members[j].Name == t.Member {
				m.Members[j].LeaderTime += t.End.Sub(t.Start)
			}
		}
	}
	for i := range m.Changes {
		if m.Changes[i].From == m.Changes[i].To {
			m.Changes[i].Kind = "retaken"
		} else {
			m.Changes[i].Kind = m.changeKind(m.Changes[i])
		}
	}
}

// dedupPause keeps a pause or resume only when it changes the cluster
// state: every member logs the same transition.
func dedupPause(events []PatroniEvent) []PatroniEvent {
	var paused bool
	out := events[:0]
	for _, ev := range events {
		if ev.Kind == "pause" || ev.Kind == "resume" {
			if (ev.Kind == "pause") == paused {
				continue
			}
			paused = ev.Kind == "pause"
		}
		out = append(out, ev)
	}
	return out
}

// dcsIncidents merges the members' DCS losses that overlap in time. A loss
// still open when the logs end runs to their end.
func dcsIncidents(events []PatroniEvent, end time.Time) []PatroniIncident {
	lost := map[string]time.Time{}
	var spans []PatroniIncident
	for _, ev := range events {
		switch ev.Kind {
		case "dcs_lost":
			lost[ev.Member] = ev.Timestamp
		case "dcs_restored":
			if t, ok := lost[ev.Member]; ok {
				spans = append(spans, PatroniIncident{t, ev.Timestamp, []string{ev.Member}})
				delete(lost, ev.Member)
			}
		}
	}
	for mb, t := range lost {
		spans = append(spans, PatroniIncident{t, end, []string{mb}})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	var out []PatroniIncident
	for _, sp := range spans {
		if n := len(out); n > 0 && !sp.Start.After(out[n-1].End) {
			last := &out[n-1]
			if sp.End.After(last.End) {
				last.End = sp.End
			}
			last.Members = append(last.Members, sp.Members...)
			sort.Strings(last.Members)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// changeKind calls a change a switchover when it was asked for: a request
// or graceful demote shortly before, or the new leader clearing the
// switchover key. Anything else is a failover.
func (m *PatroniMetrics) changeKind(c PatroniChange) string {
	for _, ev := range m.Events {
		d := c.Timestamp.Sub(ev.Timestamp)
		switch ev.Kind {
		case "switchover_request", "switchover_seen":
			if d >= 0 && d <= switchoverWindow {
				return "switchover"
			}
		case "demote":
			if d >= 0 && d <= switchoverWindow && ev.Member == c.From && strings.Contains(ev.Detail, "graceful") {
				return "switchover"
			}
		case "failover_key":
			if ev.Member == c.To && d >= -switchoverWindow && d <= switchoverWindow {
				return "switchover"
			}
		}
	}
	return "failover"
}

// CorrelatePostgres attaches the PostgreSQL lifecycle events logged from
// just before the old leader was last seen to shortly after the change,
// compared on the wall clock.
func (m *PatroniMetrics) CorrelatePostgres(timeline []ServerTimelineEvent) {
	for i := range m.Changes {
		c := &m.Changes[i]
		from, to := c.Timestamp.Add(-c.Leaderless-pgBefore), c.Timestamp.Add(pgAfter)
		for _, ev := range timeline {
			if t := parser.WallClock(ev.Timestamp); !t.Before(from) && !t.After(to) {
				c.Postgres = append(c.Postgres, ev)
			}
		}
		sort.SliceStable(c.Postgres, func(i, j int) bool { return c.Postgres[i].Timestamp.Before(c.Postgres[j].Timestamp) })
	}
}
