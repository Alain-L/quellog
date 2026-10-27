package output

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alain-L/quellog/analysis"
)

// The short labels of the pgBackRest report, shared by the text and
// markdown views (pgbackrest_view.go) and the JSON the HTML report reads
// (pgbackrest_json.go), so that every output words a fact the same way.
// No build tag: the JSON is also built by the WASM demo.

func pgbrTrunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

var (
	pgbrInfoFileRe   = regexp.MustCompile(`(archive|backup)\.info`)
	pgbrRepoPrefixRe = regexp.MustCompile(`(repo\d+): \[`)
)

// pgbrKnownErrors label the errors whose message says less than its
// code.
var pgbrKnownErrors = map[int]string{
	82: "WAL not archived in time",
	50: "lock held by another command",
	68: "archive_command lacks pgbackrest",
	38: "PostgreSQL running, --no-online refused",
	51: "PostgreSQL and stanza versions differ",
	62: "stanza stopped",
	46: "unexpected control version",
}

// pgbrKnownMessages label the errors whose code is shared by unrelated
// messages.
var pgbrKnownMessages = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`^PostgreSQL working directory .* is not the same as option pg1-path`), "pg1-path is not the data directory"},
}

// pgbrErrorLabel is the short label of an error: what failed on which
// info file, or what the code means, else the message cut short.
func pgbrErrorLabel(code int, hasCode bool, msg, repoLine string, detail []string) string {
	text := strings.Join(append([]string{msg, repoLine}, detail...), " ")
	if m := pgbrInfoFileRe.FindString(text); m != "" {
		cause := "unreadable"
		switch {
		case strings.Contains(text, "Permission denied"):
			cause = "permission denied"
		case strings.Contains(text, "FileMissingError"), strings.Contains(text, "missing file"):
			cause = "missing"
		}
		repo := ""
		if hasCode && code == 104 {
			if r := pgbrRepoPrefixRe.FindStringSubmatch(text); r != nil {
				repo = r[1] + " "
			}
		}
		return repo + m + " " + cause
	}
	if hasCode {
		if l, ok := pgbrKnownErrors[code]; ok {
			return l
		}
	}
	for _, k := range pgbrKnownMessages {
		if k.re.MatchString(msg) {
			return k.label
		}
	}
	msg = strings.TrimRight(strings.TrimSuffix(msg, " - cannot proceed"), ":")
	return pgbrTrunc(msg, 38)
}

func pgbrIssueErrorLabel(is analysis.PgBackRestIssue) string {
	return pgbrErrorLabel(is.Code, is.HasCode, is.Message, is.RepoLine, is.Detail)
}

var (
	pgbrRetentionUnsetRe = regexp.MustCompile(`option '(repo\d+-retention-(full|archive))' is not set`)
	pgbrWarningLabels    = []struct {
		re    *regexp.Regexp
		label string
	}{
		{regexp.MustCompile(`^dropped WAL file`), "WAL dropped, queue full"},
		{regexp.MustCompile(`^unable to check pg1`), "pg1 unreachable during check"},
		{regexp.MustCompile(`^invalid page checksum`), "invalid page checksum"},
		{regexp.MustCompile(`timeline switch`), "timeline switch, delta checksum on"},
		{regexp.MustCompile(`^no prior backup exists`), "incr changed to full"},
		{regexp.MustCompile(`already exists in the repo`), "WAL already archived"},
		{regexp.MustCompile(`^stop file already exists`), "stanza already stopped"},
		{regexp.MustCompile(`^stop file does not exist`), "stanza was not stopped"},
		{regexp.MustCompile(`expiring latest backup`), "latest backup expired"},
		{regexp.MustCompile(`resum`), "resumable backup found"},
		{regexp.MustCompile(`cannot alter`), "incr kept the full's options"},
	}
)

// pgbrIssueWarningLabel is the short label of a warning: a repository's
// failure ("repo1: [FileOpenError] ...") is worded as an error.
func pgbrIssueWarningLabel(is analysis.PgBackRestIssue) string {
	if pgbrRepoPrefixRe.MatchString(is.Message) && strings.HasPrefix(is.Message, "repo") {
		return pgbrErrorLabel(0, false, is.Message, "", is.Detail)
	}
	return pgbrWarningLabel(is.Message)
}

// pgbrArchiveSide names the archive command behind an issue ("push",
// "get", "async push", "async get"), "" when other or several commands
// logged it.
func pgbrArchiveSide(is analysis.PgBackRestIssue) string {
	side := ""
	for _, c := range is.Commands {
		var s string
		switch c {
		case "archive-push":
			s = "push"
		case "archive-get":
			s = "get"
		case "archive-push:async":
			s = "async push"
		case "archive-get:async":
			s = "async get"
		default:
			return ""
		}
		if side != "" && side != s {
			return ""
		}
		side = s
	}
	return side
}

// pgbrIssueLabels returns the short label of each error and warning of
// m, the archive command appended when both archive-push and archive-get
// logged issues: "archive.info permission denied, get".
func pgbrIssueLabels(m analysis.PgBackRestMetrics) (errs, warns []string) {
	push, get := false, false
	for _, list := range [][]analysis.PgBackRestIssue{m.Errors, m.Warnings} {
		for _, is := range list {
			s := pgbrArchiveSide(is)
			push = push || strings.HasSuffix(s, "push")
			get = get || strings.HasSuffix(s, "get")
		}
	}
	label := func(is analysis.PgBackRestIssue, l string) string {
		if s := pgbrArchiveSide(is); push && get && s != "" {
			return l + ", " + s
		}
		return l
	}
	for _, is := range m.Errors {
		errs = append(errs, label(is, pgbrIssueErrorLabel(is)))
	}
	for _, is := range m.Warnings {
		warns = append(warns, label(is, pgbrIssueWarningLabel(is)))
	}
	return errs, warns
}

// pgbrWarningLabel is the short label of a warning.
func pgbrWarningLabel(msg string) string {
	if m := pgbrRetentionUnsetRe.FindStringSubmatch(msg); m != nil {
		return m[1] + " not set"
	}
	for _, w := range pgbrWarningLabels {
		if w.re.MatchString(msg) {
			return w.label
		}
	}
	return pgbrTrunc(msg, 38)
}

// pgbrOutageCause is the short cause of an archive-push (cmd
// "archive-push") or archive-get outage, taken from the narrowest error
// group of that command and code that overlaps it, one of the same
// message first, else its first error line.
func pgbrOutageCause(m analysis.PgBackRestMetrics, f analysis.PgBackRestFailureStretch, cmd string) string {
	pick := func(sameMessage bool) *analysis.PgBackRestIssue {
		var cause *analysis.PgBackRestIssue
		for i, is := range m.Errors {
			if is.Code != f.Code || (sameMessage && is.Message != f.FirstLine) || is.First.IsZero() || f.First.IsZero() ||
				is.Last.Before(f.First) || f.Last.Before(is.First) ||
				!slices.ContainsFunc(is.Commands, func(c string) bool { return strings.HasPrefix(c, cmd) }) {
				continue
			}
			if cause == nil || is.Last.Sub(is.First) < cause.Last.Sub(cause.First) {
				cause = &m.Errors[i]
			}
		}
		return cause
	}
	if cause := pick(true); cause != nil {
		return pgbrIssueErrorLabel(*cause)
	}
	if cause := pick(false); cause != nil {
		return pgbrIssueErrorLabel(*cause)
	}
	return f.FirstLine
}

// pgbrRestoreRecovery says what stopped the recovery of the restore at
// index i of m.Operations: the archive-get outages that never recovered
// and the WAL found missing for its data directory (Path), between its
// end and the next restore into the same directory. "" when none.
func pgbrRestoreRecovery(m analysis.PgBackRestMetrics, i int) string {
	o := m.Operations[i]
	if o.Command != "restore" || o.Path == "" {
		return ""
	}
	var until time.Time
	for _, n := range m.Operations[i+1:] {
		if n.Command == "restore" && n.Path == o.Path {
			until = n.Time
			break
		}
	}
	in := func(t time.Time) bool { return !t.Before(o.Time) && (until.IsZero() || t.Before(until)) }
	var bits []string
	for _, f := range m.ArchiveGet.Failures {
		if f.Path == o.Path && f.Recovery == analysis.PgBackRestStillFailing && in(f.First) {
			b := "archive-get failed"
			if f.Code != 0 {
				b += fmt.Sprintf(" [%03d]", f.Code)
			}
			bits = append(bits, b)
		}
	}
	for _, w := range m.MissingWAL {
		if w.Path == o.Path && in(w.Time) {
			bits = append(bits, "WAL "+w.Segment+" missing")
		}
	}
	return strings.Join(bits, ", ")
}

var (
	pgbrRestoreSummaryRe = regexp.MustCompile(`restore backup set (\S+), .*destination: ([^,]+), target: (.*)`)
	pgbrTargetTimeRe     = regexp.MustCompile(`time=\S+ (\d\d:\d\d:\d\d)\S*`)
	pgbrStanzaOpRe       = regexp.MustCompile(`(^|; )stanza-\w+ for stanza '[^']*' on `)
	pgbrReposRe          = regexp.MustCompile(`^repo\d+(, repo\d+)*$`)
	pgbrStanzaExistsRe   = regexp.MustCompile(`^stanza '[^']*' already exists on (\S+) and is valid$`)
	pgbrCheckWALRe       = regexp.MustCompile(`WAL segment (\S+) successfully archived to '[^']*' on (\S+)`)
)

// pgbrOperationDetail shortens an operation's summary line.
func pgbrOperationDetail(o analysis.PgBackRestOperation) string {
	s := o.Summary
	switch {
	case o.Command == "restore":
		if m := pgbrRestoreSummaryRe.FindStringSubmatch(s); m != nil {
			target := m[3]
			if target == "default" {
				target = "latest"
			} else {
				target = pgbrTargetTimeRe.ReplaceAllString(target, "to $1")
			}
			return fmt.Sprintf("%s into %s, %s", m[1], m[2], target)
		}
	case strings.HasPrefix(o.Command, "stanza-"):
		s = strings.ReplaceAll(pgbrStanzaOpRe.ReplaceAllString(s, "$1"), "; ", ", ")
		if pgbrReposRe.MatchString(s) {
			s = "on " + s
		}
		s = pgbrStanzaExistsRe.ReplaceAllString(s, "already exists on $1")
	case o.Command == "check":
		s = pgbrCheckWALRe.ReplaceAllString(s, "WAL $1 archived on $2")
	}
	return s
}
