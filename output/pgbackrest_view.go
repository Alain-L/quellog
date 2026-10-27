//go:build !js

package output

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// The pgBackRest report, laid out once for the text and markdown
// renderers. The synthetic view gives, per stanza, the restore points,
// the backups, the WAL archiving and what the log cannot tell, then the
// most frequent errors. The detailed view (--pgbackrest-detail, --full)
// adds the untimed facts the synthetic view leaves out, one timeline of
// every dated event, and the errors and warnings laid out like the
// EVENTS section, each with the ID --event-detail takes.

// pgbrKV is one "label : value" line.
type pgbrKV struct{ Label, Value string }

type pgbrStanzaView struct {
	Name  string
	Lines []pgbrKV
}

// pgbrTopError is one row of the synthetic view's top errors.
type pgbrTopError struct {
	Code, Label, Stanza string
	Count               int
	When                string
}

// pgbrEvent is one row of the timeline.
type pgbrEvent struct {
	At                                            time.Time
	Stanza, Event, Duration, Size, Result, Detail string
}

type pgbrIssueRow struct {
	ID, Label, Stanza string
	Count             int
	Pct               float64
	When              string
}

// pgbrIssueGroup is the issues of one error code; Header is "" for the
// warnings, which are not grouped.
type pgbrIssueGroup struct {
	Header string
	Rows   []pgbrIssueRow
}

type pgbrSeverity struct {
	Label        string
	Total, Kinds int
	Groups       []pgbrIssueGroup
}

type pgbrView struct {
	Detail     bool
	Head       []pgbrKV
	Stanzas    []pgbrStanzaView
	TopErrors  []pgbrTopError
	MoreErrors int
	// Timeline columns: Stanza is shown for several stanzas, Size when a
	// row has one.
	Timeline       []pgbrEvent
	TimelineStanza bool
	TimelineSize   bool
	Severities     []pgbrSeverity
	clock          string
}

// pgbrTopErrorsMax caps the synthetic view's top errors.
const pgbrTopErrorsMax = 5

// pgbrDetailHint closes the synthetic view.
const pgbrDetailHint = "Details: --pgbackrest-detail[=stanza]"

func buildPgBackRestView(pbr []analysis.PgBackRestMetrics, detail bool) pgbrView {
	v := pgbrView{Detail: detail, clock: pgbrViewClock(pbr)}
	names := make([]string, len(pbr))
	for i, m := range pbr {
		names[i] = analysis.PgBackRestStanzaName(m.Stanza)
	}
	if len(names) == 1 {
		v.Head = append(v.Head, pgbrKV{"Stanza", names[0]})
	} else {
		v.Head = append(v.Head, pgbrKV{"Stanzas", fmt.Sprintf("%d (%s)", len(names), strings.Join(names, ", "))})
	}
	var versions []string
	for _, m := range pbr {
		versions = mergeSorted(versions, m.Versions)
	}
	if len(versions) > 0 {
		v.Head = append(v.Head, pgbrKV{"pgBackRest version", strings.Join(versions, ", ")})
	}
	if w := pgbrWindow(pbr); w != nil {
		v.Head = append(v.Head, pgbrKV{"Window", pgbrWindowLabel(*w)})
	}
	if detail {
		var start, end time.Time
		lines := 0
		files := map[string]bool{}
		for _, m := range pbr {
			if !m.StartTime.IsZero() && (start.IsZero() || m.StartTime.Before(start)) {
				start = m.StartTime
			}
			if m.EndTime.After(end) {
				end = m.EndTime
			}
			lines += m.LinesRead
			for _, f := range m.Files {
				files[f] = true
			}
		}
		if !start.IsZero() {
			v.Head = append(v.Head, pgbrKV{"Period", start.Format("2006-01-02 15:04:05") + " → " + v.at(end) + ", " + pgbrDur(end.Sub(start))})
		}
		v.Head = append(v.Head, pgbrKV{"Read", fmt.Sprintf("%s in %s", pgbrPlural(lines, "line"), pgbrPlural(len(files), "file"))})
	}

	for _, m := range pbr {
		v.Stanzas = append(v.Stanzas, pgbrStanzaView{Name: analysis.PgBackRestStanzaName(m.Stanza), Lines: v.stanzaLines(m)})
	}

	if !detail {
		v.topErrors(pbr)
		return v
	}
	stanzas := map[string]bool{}
	for _, m := range pbr {
		for _, e := range v.events(m) {
			v.Timeline = append(v.Timeline, e)
			stanzas[e.Stanza] = true
			v.TimelineSize = v.TimelineSize || e.Size != ""
		}
	}
	v.TimelineStanza = len(pbr) > 1
	sort.SliceStable(v.Timeline, func(i, j int) bool {
		a, b := v.Timeline[i].At, v.Timeline[j].At
		if a.IsZero() != b.IsZero() {
			return b.IsZero()
		}
		return a.Before(b)
	})
	v.Severities = pgbrSeverities(pbr, v.when)
	return v
}

func mergeSorted(a, b []string) []string {
	for _, s := range b {
		if !slices.Contains(a, s) {
			a = append(a, s)
		}
	}
	sort.Strings(a)
	return a
}

// pgbrWindow is the --begin/--end window of the reports, cut on a side
// when any stanza's lines were, or nil without one.
func pgbrWindow(pbr []analysis.PgBackRestMetrics) *analysis.PgBackRestWindow {
	var w *analysis.PgBackRestWindow
	for _, m := range pbr {
		if m.Window == nil {
			continue
		}
		if w == nil {
			c := *m.Window
			w = &c
		}
		w.CutBefore = w.CutBefore || m.Window.CutBefore
		w.CutAfter = w.CutAfter || m.Window.CutAfter
	}
	return w
}

func pgbrWindowLabel(w analysis.PgBackRestWindow) string {
	const layout = "2006-01-02 15:04:05"
	var s string
	switch {
	case w.Begin.IsZero():
		s = "until " + w.End.Format(layout)
	case w.End.IsZero():
		s = "from " + w.Begin.Format(layout)
	default:
		s = w.Begin.Format(layout) + " → " + w.End.Format(layout)
	}
	switch {
	case w.CutBefore && w.CutAfter:
		s += " (lines before and after it left out)"
	case w.CutBefore:
		s += " (lines before it left out)"
	case w.CutAfter:
		s += " (lines after it left out)"
	}
	return s
}

// pgbrNoneLabel is "none", or "none in this window" when the window
// left lines of m out.
func pgbrNoneLabel(m analysis.PgBackRestMetrics) string {
	if m.Window != nil && (m.Window.CutBefore || m.Window.CutAfter) {
		return "none in this window"
	}
	return "none"
}

// pgbrViewClock is the time layout of the whole report: "15:04:05" when
// every stanza's activity fits in one calendar day, else the full date.
func pgbrViewClock(pbr []analysis.PgBackRestMetrics) string {
	var day string
	for _, m := range pbr {
		for _, t := range []time.Time{m.StartTime, m.EndTime} {
			if t.IsZero() {
				continue
			}
			d := t.Format("2006-01-02")
			if day != "" && d != day {
				return "2006-01-02 15:04:05"
			}
			day = d
		}
	}
	return "15:04:05"
}

// at renders t in the report's layout, "?" when undated.
func (v *pgbrView) at(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.Format(v.clock)
}

// when is "first → last", or first alone when they read the same.
func (v *pgbrView) when(first, last time.Time) string {
	if first.IsZero() {
		return ""
	}
	w := v.at(first)
	if l := v.at(last); !last.IsZero() && l != w {
		w += " → " + l
	}
	return w
}

// pgbrDur renders a span: "42s", "3m05s", "2h07m".
func pgbrDur(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	}
}

// pgbrRunDur renders a run's duration, in ms under a second.
func pgbrRunDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return pgbrDur(d)
}

// pgbrPlural renders "1 file", "2 files".
func pgbrPlural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

var pgbrSizeRe = regexp.MustCompile(`^([\d.]+)([KMGTP]?B)$`)

// pgbrSize renders pgBackRest's "29.4MB" as "29.4 MB".
func pgbrSize(s string) string {
	if m := pgbrSizeRe.FindStringSubmatch(s); m != nil {
		return m[1] + " " + m[2]
	}
	return s
}

var pgbrRecoveryWords = map[string]string{
	analysis.PgBackRestRecovered:       "recovered",
	analysis.PgBackRestStillFailing:    "still failing",
	analysis.PgBackRestNoLaterPush:     "pushed by another stanza",
	analysis.PgBackRestRecoveryUnknown: "outcome unknown",
}

// pgbrExpiredAt is when an expire run removed backup label.
func (v *pgbrView) expiredAt(m analysis.PgBackRestMetrics, label string) string {
	for _, r := range m.ExpireRuns {
		if r.SetLabel == label || slices.Contains(r.Expired, label) {
			return v.at(r.Time)
		}
	}
	return "?"
}

var pgbrRestoreTargetRe = regexp.MustCompile(`target: time=\S+ (\d\d:\d\d:\d\d)`)

func (v *pgbrView) stanzaLines(m analysis.PgBackRestMetrics) []pgbrKV {
	var out []pgbrKV
	add := func(label, value string) { out = append(out, pgbrKV{label, value}) }

	// Restore points, with their repository when the backups use several.
	multiRepo := pgbrMultiRepo(m.Backups)
	label := func(b analysis.PgBackRestBackup) string {
		if multiRepo && b.Repo != "" {
			return b.Label + " (" + b.Repo + ")"
		}
		return b.Label
	}
	if b, _, ok := m.LastOf("full"); ok {
		add("Last full", label(b))
	} else if exp := m.Last["full"].Expired; exp != "" {
		add("Last full", "none (expired at "+v.expiredAt(m, exp)+")")
	} else if m.BackupsLogged || len(m.Backups) > 0 {
		add("Last full", pgbrNoneLabel(m))
	} else {
		add("Last full", "not logged")
	}
	var kinds, values []string
	for _, kind := range []string{"diff", "incr"} {
		if b, _, ok := m.LastOf(kind); ok {
			kinds, values = append(kinds, kind), append(values, label(b))
		} else if exp := m.Last[kind].Expired; exp != "" {
			kinds, values = append(kinds, kind), append(values, "none (expired at "+v.expiredAt(m, exp)+")")
		}
	}
	if len(kinds) == 2 && values[0] == values[1] {
		add("Last diff, incr", values[0])
	} else {
		for i, k := range kinds {
			add("Last "+k, values[i])
		}
	}

	// Backups.
	if len(m.Backups) > 0 {
		var ok, failed, killed, running int
		var lastBad *analysis.PgBackRestBackup
		for i, b := range m.Backups {
			switch {
			case b.Succeeded:
				ok++
			case b.InProgress:
				running++
			case b.Crashed:
				killed++
				lastBad = &m.Backups[i]
			default:
				failed++
				lastBad = &m.Backups[i]
			}
		}
		var parts []string
		for _, p := range []struct {
			n    int
			word string
		}{{ok, "ok"}, {failed, "failed"}, {killed, "killed"}, {running, "in progress"}} {
			if p.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
			}
		}
		add("Backups", strings.Join(parts, ", "))
		if lastBad != nil {
			at := lastBad.End
			if at.IsZero() {
				at = lastBad.Start
			}
			add("Last failure", lastBad.Type+" at "+v.at(at))
		}
	}

	// WAL archiving.
	ap := m.ArchivePush
	if ap.Logged || ap.AsyncLogged {
		val := fmt.Sprintf("%d pushed, %d failed", ap.Pushed+ap.PushedAsync, ap.Failed)
		if ap.LastPush != nil && !ap.LastPush.Time.IsZero() {
			val += ", last at " + v.at(ap.LastPush.Time)
		}
		// Failed runs that logged no begin are counted by PostgreSQL only
		// (the detailed view states them on their own line).
		if n := len(m.PGArchiveFailures); !v.Detail && n > ap.Failed {
			val += fmt.Sprintf(" (PostgreSQL: %d archive_command %s)", n, pgbrNoun(n, "failure"))
		}
		add("WAL archiving", val)
	} else {
		val := "not logged"
		if n := len(m.PGArchiveFailures); n > 0 {
			val += fmt.Sprintf(" (PostgreSQL: %d archive_command %s)", n, pgbrNoun(n, "failure"))
		}
		add("WAL archiving", val)
	}
	if v.Detail {
		if ap.PushedAsync > 0 || ap.FailedAsync > 0 {
			add("Async worker", fmt.Sprintf("%d pushed, %d failed", ap.PushedAsync, ap.FailedAsync))
		}
		if pf := m.PGArchiveFailures; len(pf) > 0 && (ap.Logged || ap.AsyncLogged) {
			val := fmt.Sprintf("%d archive_command %s (%s)", len(pf), pgbrNoun(len(pf), "failure"), pgbrExitCodes(pf))
			if n := m.PGRetryWarnings; n > 0 {
				val += fmt.Sprintf(", gave up %s for now", pgbrPlural(n, "time"))
			}
			add("Seen by PostgreSQL", val)
		}
	}
	if st := ap.Failures; len(st) > 0 {
		states := map[string]int{}
		var order []string
		var longest time.Duration
		spans := false
		for _, f := range st {
			if states[f.Recovery] == 0 {
				order = append(order, f.Recovery)
			}
			states[f.Recovery]++
			if !f.First.IsZero() && !f.Last.IsZero() {
				spans = true
				longest = max(longest, f.Last.Sub(f.First))
			}
		}
		var val string
		if len(st) == 1 {
			val = "1"
			if spans {
				val += " (" + pgbrDur(longest) + ")"
			}
			val += ", " + pgbrRecoveryWords[st[0].Recovery]
		} else {
			var parts []string
			if len(order) == 1 {
				parts = append(parts, "all "+pgbrRecoveryWords[order[0]])
			} else {
				for _, k := range order {
					parts = append(parts, fmt.Sprintf("%d %s", states[k], pgbrRecoveryWords[k]))
				}
			}
			if spans {
				parts = append(parts, "longest "+pgbrDur(longest))
			}
			val = fmt.Sprintf("%d, %s", len(st), strings.Join(parts, ", "))
		}
		add("Archiving outages", val)
	}
	if drops := ap.DroppedEvents; len(drops) > 0 {
		word := "dropped"
		if len(drops) > 1 {
			word = "last dropped"
		}
		add("WAL lost", fmt.Sprintf("%d, %s at %s (queue full)", len(drops), word, v.at(drops[len(drops)-1].Time)))
	}
	// WAL restored by archive-get, and the segments it found missing.
	if ag := m.ArchiveGet; (ag.Logged || ag.AsyncLogged) && ag.Found+ag.FoundAsync+ag.Failed+ag.FailedAsync > 0 {
		val := strconv.Itoa(ag.Found + ag.FoundAsync)
		if ag.FoundAsync > 0 {
			val += fmt.Sprintf(" (%d async)", ag.FoundAsync)
		}
		val += fmt.Sprintf(", %d failed", ag.Failed+ag.FailedAsync)
		if !ag.LastFound.IsZero() {
			val += ", last at " + v.at(ag.LastFound)
		}
		add("WAL restored", val)
	}
	if mw := m.MissingWAL; len(mw) > 0 {
		segs := map[string]bool{}
		for _, w := range mw {
			segs[w.Segment] = true
		}
		val := mw[0].Segment + ", pushed at " + v.at(mw[0].PushedAt) + ", not found at " + v.at(mw[0].Time)
		if len(segs) > 1 {
			val = fmt.Sprintf("%d, first %s", len(segs), val)
		}
		add("Missing WAL", val)
	}

	// Retention.
	if v.Detail && (len(m.ExpireRuns) > 0 || len(m.RetentionWarnings) > 0) {
		var bits []string
		removed, bad := 0, 0
		for _, r := range m.ExpireRuns {
			if len(r.Expired) > 0 || r.ArchiveRemoved > 0 {
				removed++
			}
			if !r.Succeeded {
				bad++
			}
		}
		if n := len(m.ExpireRuns); n > 0 {
			bits = append(bits, pgbrPlural(n, "expire run"))
			if bad > 0 {
				bits = append(bits, fmt.Sprintf("%d failed", bad))
			}
			if removed == 0 {
				bits = append(bits, "nothing removed")
			}
		}
		archiveRemoved := slices.ContainsFunc(m.ExpireRuns, func(r analysis.PgBackRestExpireRun) bool { return r.ArchiveRemoved > 0 })
		for _, w := range m.RetentionWarnings {
			// An expire run given a retention (--repoN-retention-full) still
			// removes WAL: the warning then says nothing of the log.
			if l := pgbrWarningLabel(w.Text); strings.HasSuffix(l, "retention-archive not set") && !archiveRemoved {
				bits = append(bits, l+", WAL never expired")
			}
		}
		if len(bits) > 0 {
			add("Retention", strings.Join(bits, ", "))
		}
	}

	// Restores and the other operations.
	var restores []analysis.PgBackRestOperation
	stopped := 0
	for i, o := range m.Operations {
		if o.Command == "restore" {
			restores = append(restores, o)
			if pgbrRestoreRecovery(m, i) != "" {
				stopped++
			}
		}
	}
	if len(restores) > 0 {
		o := restores[len(restores)-1]
		target := " (latest)"
		if t := pgbrRestoreTargetRe.FindStringSubmatch(o.Summary); t != nil {
			target = " (to " + t[1] + ")"
		}
		bad := 0
		for _, r := range restores {
			if !r.Succeeded {
				bad++
			}
		}
		val := strconv.Itoa(len(restores))
		if bad > 0 {
			val += fmt.Sprintf(" (%d failed)", bad)
		}
		val += ", last at " + v.at(o.Time) + target
		if stopped > 0 {
			val += fmt.Sprintf(", %d with WAL restore failures", stopped)
		}
		add("Restores", val)
	}
	notable := map[string]string{}
	var notableOrder []string
	for _, o := range m.Operations {
		switch o.Command {
		case "stop", "start", "stanza-upgrade", "stanza-delete":
			if _, seen := notable[o.Command]; !seen {
				notableOrder = append(notableOrder, o.Command)
			}
			notable[o.Command] = v.at(o.Time)
		}
	}
	if len(notableOrder) > 0 {
		parts := make([]string, len(notableOrder))
		for i, c := range notableOrder {
			parts[i] = c + " at " + notable[c]
		}
		add("Operations", strings.Join(parts, ", "))
	}
	otherBad := map[string]int{}
	var otherOrder []string
	noteBad := func(cmd string) {
		if otherBad[cmd] == 0 {
			otherOrder = append(otherOrder, cmd)
		}
		otherBad[cmd]++
	}
	for _, o := range m.Operations {
		if !o.Succeeded && o.HasOutcome {
			noteBad(o.Command)
		}
	}
	for _, r := range m.ExpireRuns {
		if !r.Succeeded {
			noteBad("expire")
		}
	}
	if len(otherOrder) > 0 {
		parts := make([]string, len(otherOrder))
		for i, c := range otherOrder {
			parts[i] = fmt.Sprintf("%s %d", c, otherBad[c])
		}
		add("Other failures", strings.Join(parts, ", "))
	}

	// What the log cannot tell.
	for _, note := range m.MissingInfo {
		switch {
		case strings.HasPrefix(note, "log-timestamp=n"):
			add("Not in the log", "timestamps (log-timestamp=n)")
		case strings.HasPrefix(note, "no command begin/end lines seen"):
			add("Not in the log", "run outcomes (log-level-file below info)")
		default:
			n, _ := strconv.Atoi(strings.Fields(note)[0])
			add("Not in the log", fmt.Sprintf("%d run %s (log-level-file)", n, pgbrNoun(n, "outcome")))
		}
	}
	return out
}

// pgbrMultiRepo reports whether these backups span more than one
// repository.
func pgbrMultiRepo(backups []analysis.PgBackRestBackup) bool {
	seen := ""
	for _, b := range backups {
		if b.Repo != "" && seen != "" && b.Repo != seen {
			return true
		}
		if b.Repo != "" {
			seen = b.Repo
		}
	}
	return false
}

// pgbrNoun is noun, plural when n is not 1.
func pgbrNoun(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// pgbrExitCodes renders the distinct causes of PostgreSQL's
// archive_command failures: "exit 1, 103", "signal 9".
func pgbrExitCodes(fs []analysis.PgBackRestPGArchiveFailure) string {
	var exits, signals []int
	for _, f := range fs {
		switch {
		case f.IsSignal && !slices.Contains(signals, f.Signal):
			signals = append(signals, f.Signal)
		case f.HasExit && !slices.Contains(exits, f.ExitCode):
			exits = append(exits, f.ExitCode)
		}
	}
	sort.Ints(exits)
	sort.Ints(signals)
	join := func(word string, ns []int) string {
		s := make([]string, len(ns))
		for i, n := range ns {
			s[i] = strconv.Itoa(n)
		}
		return word + " " + strings.Join(s, ", ")
	}
	var parts []string
	if len(exits) > 0 {
		parts = append(parts, join("exit", exits))
	}
	if len(signals) > 0 {
		parts = append(parts, join("signal", signals))
	}
	if len(parts) == 0 {
		return "exit unknown"
	}
	return strings.Join(parts, ", ")
}

func (v *pgbrView) topErrors(pbr []analysis.PgBackRestMetrics) {
	var all []pgbrTopError
	for _, m := range pbr {
		labels, _ := pgbrIssueLabels(m)
		for i, is := range m.Errors {
			code := ""
			if is.HasCode {
				code = fmt.Sprintf("[%03d]", is.Code)
			}
			all = append(all, pgbrTopError{Code: code, Label: labels[i],
				Stanza: analysis.PgBackRestStanzaName(m.Stanza), Count: is.Count, When: v.when(is.First, is.Last)})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Count > all[j].Count })
	if len(all) > pgbrTopErrorsMax {
		v.MoreErrors = len(all) - pgbrTopErrorsMax
		all = all[:pgbrTopErrorsMax]
	}
	v.TopErrors = all
}

func (v *pgbrView) events(m analysis.PgBackRestMetrics) []pgbrEvent {
	stanza := analysis.PgBackRestStanzaName(m.Stanza)
	var ev []pgbrEvent
	add := func(at time.Time, event, dur, size, result, detail string) {
		ev = append(ev, pgbrEvent{At: at, Stanza: stanza, Event: event, Duration: dur, Size: size, Result: result, Detail: detail})
	}
	multiRepo := pgbrMultiRepo(m.Backups)
	for _, b := range m.Backups {
		dur := ""
		if b.HasDuration {
			dur = pgbrRunDur(b.Duration)
		}
		var result, detail, size string
		switch {
		case b.Succeeded:
			result, detail = "ok", b.Label
			if b.HasSize {
				size = pgbrSize(b.SizeRaw)
			}
		case b.InProgress:
			result = "in progress"
		case b.Crashed:
			result = "killed"
			var bits []string
			if b.Signal != "" {
				bits = append(bits, b.Signal)
			}
			if !b.LastLine.IsZero() {
				bits = append(bits, "last line at "+v.at(b.LastLine))
			}
			detail = strings.Join(bits, ", ")
		default:
			result = "failed"
			if b.ErrorCode != 0 {
				result = fmt.Sprintf("failed [%03d]", b.ErrorCode)
			}
			if b.ErrorLine != "" || b.ErrorCode != 0 {
				detail = pgbrErrorLabel(b.ErrorCode, b.ErrorCode != 0, b.ErrorLine, "", b.ErrorDetail)
			}
		}
		var also []string
		if b.ChangedToFull && b.RequestedType != "" {
			also = append(also, "requested "+b.RequestedType)
		}
		if multiRepo && b.Repo != "" {
			also = append(also, "on "+b.Repo)
		}
		if len(also) > 0 {
			detail = strings.TrimPrefix(detail+", "+strings.Join(also, ", "), ", ")
		}
		add(b.Start, "backup "+b.Type, dur, size, result, detail)
	}
	for _, f := range m.ArchivePush.Failures {
		ev = append(ev, v.outageEvent(stanza, m, f, "archive-push"))
	}
	for _, f := range m.ArchiveGet.Failures {
		ev = append(ev, v.outageEvent(stanza, m, f, "archive-get"))
	}
	for _, w := range m.MissingWAL {
		detail := w.Segment + ", pushed at " + v.at(w.PushedAt)
		if w.Path != "" {
			detail += ", " + w.Path
		}
		add(w.Time, "missing WAL", "", "", "not found", detail)
	}
	for _, d := range m.ArchivePush.DroppedEvents {
		add(d.Time, "WAL lost", "", "", "dropped", d.Segment+", archive queue full")
	}
	for i, o := range m.Operations {
		dur := ""
		if o.HasDuration {
			dur = pgbrRunDur(o.Duration)
		}
		result := "ok"
		if !o.Succeeded {
			switch {
			case !o.HasOutcome:
				result = "no outcome"
			case o.ErrorCode != 0:
				result = fmt.Sprintf("failed [%03d]", o.ErrorCode)
			default:
				result = "failed"
			}
		}
		detail := pgbrOperationDetail(o)
		if r := pgbrRestoreRecovery(m, i); r != "" {
			detail += "; recovery: " + r
		}
		add(o.Time, o.Command, dur, "", result, detail)
	}
	for _, r := range m.ExpireRuns {
		var bits []string
		if len(r.Expired) > 0 {
			bits = append(bits, strings.Join(r.Expired, ", "))
		}
		if r.ArchiveRemoved > 0 {
			bits = append(bits, pgbrPlural(r.ArchiveRemoved, "WAL range"))
		}
		if len(bits) == 0 && r.Succeeded {
			continue
		}
		result, detail := "ok", ""
		if !r.Succeeded {
			result = "failed"
		}
		if len(bits) > 0 {
			detail = "removed " + strings.Join(bits, "; ")
		} else if r.Repo != "" {
			detail = "on " + r.Repo
		}
		add(r.Time, "expire", "", "", result, detail)
	}
	return ev
}

var pgbrTimelineRecovery = map[string]string{
	analysis.PgBackRestRecovered:       "recovered",
	analysis.PgBackRestStillFailing:    "still failing",
	analysis.PgBackRestNoLaterPush:     "pushed elsewhere",
	analysis.PgBackRestRecoveryUnknown: "outcome unknown",
}

// outageEvent is one archive-push or archive-get (cmd) outage: its
// segment, attempts and cause (pgbrOutageCause), and for archive-get the
// data directory whose recovery it held up.
func (v *pgbrView) outageEvent(stanza string, m analysis.PgBackRestMetrics, f analysis.PgBackRestFailureStretch, cmd string) pgbrEvent {
	span := ""
	if !f.First.IsZero() && !f.Last.IsZero() {
		span = pgbrDur(f.Last.Sub(f.First))
	}
	label := pgbrOutageCause(m, f, cmd)
	if f.Code != 0 {
		label = fmt.Sprintf("[%03d] %s", f.Code, label)
	}
	detail := fmt.Sprintf("%s, %s, %s", f.Segment, pgbrPlural(f.Attempts, "attempt"), label)
	event := "archiving outage"
	if cmd == "archive-get" {
		event = "WAL restore outage"
		if f.Path != "" {
			detail += ", " + f.Path
		}
	}
	return pgbrEvent{At: f.First, Stanza: stanza, Event: event, Duration: span,
		Result: pgbrTimelineRecovery[f.Recovery], Detail: detail}
}

// pgbrSeverities lays the errors and the warnings out like the EVENTS
// section: the errors grouped by code, the groups and their rows by
// count.
func pgbrSeverities(pbr []analysis.PgBackRestMetrics, when func(first, last time.Time) string) []pgbrSeverity {
	type item struct {
		stanza string
		is     analysis.PgBackRestIssue
		label  string
	}
	var errs, warns []item
	for _, m := range pbr {
		name := analysis.PgBackRestStanzaName(m.Stanza)
		el, wl := pgbrIssueLabels(m)
		for i, is := range m.Errors {
			errs = append(errs, item{name, is, el[i]})
		}
		for i, is := range m.Warnings {
			warns = append(warns, item{name, is, wl[i]})
		}
	}
	build := func(label string, items []item, grouped bool) []pgbrSeverity {
		if len(items) == 0 {
			return nil
		}
		total := 0
		for _, it := range items {
			total += it.is.Count
		}
		groups := map[string][]item{}
		var keys []string
		for _, it := range items {
			k := ""
			if grouped {
				k = "no code"
				if it.is.HasCode {
					k = fmt.Sprintf("%03d", it.is.Code)
					if name := analysis.PgBackRestErrorName(it.is.Code); name != "" {
						k += " - " + name
					}
				}
			}
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], it)
		}
		sum := func(k string) int {
			n := 0
			for _, it := range groups[k] {
				n += it.is.Count
			}
			return n
		}
		sort.SliceStable(keys, func(i, j int) bool { return sum(keys[i]) > sum(keys[j]) })
		sev := pgbrSeverity{Label: label, Total: total, Kinds: len(items)}
		for _, k := range keys {
			its := groups[k]
			sort.SliceStable(its, func(i, j int) bool { return its[i].is.Count > its[j].is.Count })
			g := pgbrIssueGroup{Header: k}
			for _, it := range its {
				g.Rows = append(g.Rows, pgbrIssueRow{ID: it.is.ID, Label: it.label, Stanza: it.stanza,
					Count: it.is.Count, Pct: 100 * float64(it.is.Count) / float64(total), When: when(it.is.First, it.is.Last)})
			}
			sev.Groups = append(sev.Groups, g)
		}
		return []pgbrSeverity{sev}
	}
	out := build("ERROR", errs, true)
	return append(out, build("WARN", warns, false)...)
}
