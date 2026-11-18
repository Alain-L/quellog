package analysis

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// splitPgBackRestMessage decodes a pgBackRest Message ("LEVEL: text" or
// "LEVEL: [NNN]: text", optionally with "\n"-joined continuation lines)
// as the stderr parser carves it out. HINT lines are dropped: the report
// states what the log says, not what to do about it.
func splitPgBackRestMessage(msg string) (level string, code int, hasCode bool, text string, continuation []string) {
	nl := strings.IndexByte(msg, '\n')
	first := msg
	if nl >= 0 {
		first = msg[:nl]
		for _, l := range strings.Split(msg[nl+1:], "\n") {
			if !strings.HasPrefix(l, "HINT: ") {
				continuation = append(continuation, l)
			}
		}
	}
	idx := strings.IndexByte(first, ':')
	if idx < 0 {
		return "", 0, false, "", nil
	}
	lvl := first[:idx]
	if !parser.IsPgBackRestLevel(lvl) {
		return "", 0, false, "", nil
	}
	rest := strings.TrimPrefix(first[idx+1:], " ")
	if len(rest) >= 6 && rest[0] == '[' && rest[4] == ']' && rest[5] == ':' && isDigits3(rest[1:4]) {
		code, _ = strconv.Atoi(rest[1:4]) // three digits: cannot fail
		hasCode = true
		rest = strings.TrimPrefix(rest[6:], " ")
	}
	return lvl, code, hasCode, rest, continuation
}

func isDigits3(s string) bool {
	return len(s) == 3 && isDigit(s[0]) && isDigit(s[1]) && isDigit(s[2])
}

// parseCommandBegin recognizes "<cmd> command begin <version>: <options>",
// pgBackRest's own wording for every command invocation.
func parseCommandBegin(text string) (command, version, options string, ok bool) {
	idx := strings.Index(text, " command begin ")
	if idx < 0 {
		return "", "", "", false
	}
	command = text[:idx]
	rest := text[idx+len(" command begin "):]
	vIdx := strings.IndexByte(rest, ':')
	if vIdx < 0 {
		return command, rest, "", true
	}
	version = rest[:vIdx]
	options = strings.TrimPrefix(rest[vIdx+1:], " ")
	return command, version, options, true
}

// parseCommandEnd recognizes "<cmd> command end: completed successfully[
// (Nms)]" or "<cmd> command end: aborted with exception [NNN]"
// pgBackRest's own wording for every command invocation.
func parseCommandEnd(text string) (command string, success bool, ms int64, hasMs bool, code int, hasCode bool, ok bool) {
	idx := strings.Index(text, " command end: ")
	if idx < 0 {
		return "", false, 0, false, 0, false, false
	}
	command = text[:idx]
	rest := text[idx+len(" command end: "):]
	switch {
	case strings.HasPrefix(rest, "completed successfully"):
		success, ok = true, true
		if i := strings.IndexByte(rest, '('); i >= 0 && strings.HasSuffix(rest, "ms)") {
			if v, err := strconv.ParseInt(rest[i+1:len(rest)-3], 10, 64); err == nil {
				ms, hasMs = v, true
			}
		}
	case strings.HasPrefix(rest, "aborted with exception ["):
		ok = true
		if end := strings.IndexByte(rest, ']'); end > 0 {
			if v, err := strconv.Atoi(rest[len("aborted with exception ["):end]); err == nil {
				code, hasCode = v, true
			}
		}
	default:
		// Some other error message text reached cmdEnd (rare) — still a
		// failed end, with no recognizable code.
		ok = true
	}
	return
}

// extractBeginSegment pulls the WAL file name out of a sync archive-push
// begin line ("[pg_wal/SEGMENT]"), which its ERROR rarely repeats. It is
// "" for the async worker's bracketed pg_wal directory (its segments come
// from the "push N WAL file(s)" line) and for a batch archive-get.
func extractBeginSegment(options string) string {
	if len(options) == 0 || options[0] != '[' {
		return ""
	}
	end := strings.IndexByte(options, ']')
	if end < 0 {
		return ""
	}
	// archive-get names its target first: "[SEGMENT, pg_wal/RECOVERYXLOG]".
	arg, _, _ := strings.Cut(options[1:end], ", ")
	return segmentOrEmpty(strings.TrimPrefix(arg, "pg_wal/"))
}

// segmentOrEmpty returns token unchanged when it looks like a WAL
// segment or history file name, else "" — the guard that keeps a
// directory path (the async worker's begin bracket) from being
// mistaken for one.
func segmentOrEmpty(token string) string {
	if looksLikeVariableToken(token) {
		return token
	}
	return ""
}

// extractPushPullSegment pulls the first WAL file name out of "push N
// WAL file(s) to archive: X" / "get N WAL file(s) from archive: X" (a
// single name, or the first of a "X...Y" range) — the async worker's
// real per-batch target, which its begin line's bracket does not name
// (see extractBeginSegment).
func extractPushPullSegment(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	if i := strings.Index(rest, "..."); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, ','); i >= 0 {
		rest = rest[:i]
	}
	return segmentOrEmpty(strings.TrimSpace(rest))
}

// pgbrRepoLineRe matches a multi-repo aggregate error's "repoN: ..."
// continuation line (e.g. [104] "archive-push command encountered
// error(s):"), which names which repo actually broke — the one piece of
// this message's continuation that is not generic boilerplate.
var pgbrRepoLineRe = regexp.MustCompile(`^repo\d+: `)

// firstRepoLine returns the first continuation line naming the repo a
// multi-repo aggregate error came from, or "" if none — without it, a
// [104] error shown in the report never says which repo failed.
func firstRepoLine(continuation []string) string {
	for _, line := range continuation {
		if pgbrRepoLineRe.MatchString(line) {
			return line
		}
	}
	return ""
}

// annotateIssueText builds the display text (fed to both the grouping
// normalizer and ErrorLine), the Example (display text plus the repo
// line, when the message carried one), and the bare repo line on its
// own (also returned by Example), for one ERROR/WARN line.
func annotateIssueText(text string, continuation []string) (display, example, repoLine string) {
	display = text
	example = display
	repoLine = firstRepoLine(continuation)
	if repoLine != "" {
		example += "\n" + repoLine
	}
	return display, example, repoLine
}

// parseOption extracts "--key=value" from a pgBackRest command-begin
// options string (space-separated). A value pgBackRest quoted because it
// holds spaces ("--target=\"2026-10-10 13:11:10\"") is returned whole,
// without the quotes.
func parseOption(options, key string) (string, bool) {
	needle := "--" + key + "="
	idx := strings.Index(options, needle)
	if idx < 0 {
		return "", false
	}
	rest := options[idx+len(needle):]
	if len(rest) > 0 && rest[0] == '"' {
		if end := strings.IndexByte(rest[1:], '"'); end >= 0 {
			return rest[1 : end+1], true
		}
	}
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// PgBackRestAllRepos is the repository of an expire run given no --repo
// while several repositories are configured: pgBackRest expires each of
// them.
const PgBackRestAllRepos = "all"

// commandRepo returns the repository a command run works on: --repo=N,
// else PgBackRestAllRepos for an expire over several repositories, else repo1.
func commandRepo(cmd, options string) string {
	if n, ok := parseOption(options, "repo"); ok {
		return "repo" + n
	}
	if baseCommand(cmd) == "expire" && configuredRepos(options) > 1 {
		return PgBackRestAllRepos
	}
	return "repo1"
}

// configuredRepos counts the repositories the "--repoN-..." options name.
func configuredRepos(options string) int {
	seen := map[string]bool{}
	for _, f := range strings.Fields(options) {
		rest, ok := strings.CutPrefix(f, "--repo")
		if !ok {
			continue
		}
		i := digitRun(rest)
		if i > 0 && i < len(rest) && rest[i] == '-' {
			seen[rest[:i]] = true
		}
	}
	return len(seen)
}

// baseCommand strips a ":role" suffix ("archive-push:async" → "archive-push").
func baseCommand(cmd string) string {
	if i := strings.IndexByte(cmd, ':'); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// extractDoubleQuoted returns the first "..." token of s, "" if none.
func extractDoubleQuoted(s string) string {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '"')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

// extractQuoted returns the content of the first single-quoted
// substring, or "" if none.
func extractQuoted(s string) string {
	i := strings.IndexByte(s, '\'')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '\'')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

// extractSegmentHint returns a WAL-segment-looking token from a message:
// a quoted value, or else a long hex-looking word.
func extractSegmentHint(s string) string {
	if q := extractQuoted(s); q != "" {
		return q
	}
	for _, f := range strings.Fields(s) {
		f = strings.TrimRight(f, ":,.")
		if len(f) >= 16 && isHexUpper(f) {
			return f
		}
	}
	return ""
}

func isHexUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// digitRun returns the length of the run of decimal digits at the start
// of s.
func digitRun(s string) int {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

// leadingInt parses the run of decimal digits at the start of s.
func leadingInt(s string) int {
	v, _ := strconv.Atoi(s[:digitRun(s)])
	return v
}

// normalizePgBackRestMessage rewrites the variable tokens of a message so
// identical errors group: a path keeps its last two segments (the same
// cause names the same path), a per-occurrence token (WAL segment, backup
// label) becomes "...", quoted or not.
func normalizePgBackRestMessage(s string) string {
	return normalizeBareVariableTokens(MapQuotedTokens(s, shortenToken))
}

// MapQuotedTokens rewrites every single-quoted token of s with f. Exported
// for output, which shortens the paths of the lines it shows.
func MapQuotedTokens(s string, f func(string) string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\'' {
			if j := strings.IndexByte(s[i+1:], '\''); j >= 0 {
				b.WriteByte('\'')
				b.WriteString(f(s[i+1 : i+1+j]))
				b.WriteByte('\'')
				i += j + 2
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// pgbrBareVariableTokenRe matches an unquoted WAL segment name (exactly
// 24 hex characters, so a long number such as a system identifier stays
// readable) or backup label. [082] names its segment unquoted, and two
// timeouts on two segments must still group.
var pgbrBareVariableTokenRe = regexp.MustCompile(`\b(?:[0-9A-F]{24}|` + pgbrBackupLabelGrammar + `)\b`)

// pgbrBarePathRe matches an unquoted absolute path, with its leading
// blank.
var pgbrBarePathRe = regexp.MustCompile(`(?:^|\s)/[^\s'"]+`)

// normalizeBareVariableTokens placeholders the unquoted WAL segments and
// backup labels the quote-aware pass left, and shortens unquoted paths.
func normalizeBareVariableTokens(s string) string {
	s = pgbrBareVariableTokenRe.ReplaceAllString(s, "...")
	return pgbrBarePathRe.ReplaceAllStringFunc(s, func(m string) string {
		i := strings.IndexByte(m, '/')
		path := strings.TrimRight(m[i:], ".,:;)")
		return m[:i] + ShortenPath(path) + m[i+len(path):]
	})
}

// ShortenPath keeps the last two segments of a path with more than two
// (".../foo/bar") and returns any other token unchanged. Exported for
// output, so both shorten paths the same way.
func ShortenPath(token string) string {
	if !strings.Contains(token, "/") {
		return token
	}
	// "/bin/false" has two segments, not three.
	parts := strings.Split(strings.TrimPrefix(token, "/"), "/")
	if len(parts) > 2 {
		return ".../" + strings.Join(parts[len(parts)-2:], "/")
	}
	return token
}

// shortenToken is normalizePgBackRestMessage's per-token decision: a
// path keeps its last two segments, a variable-looking token (WAL
// segment, backup label) becomes a placeholder, anything else (a
// stanza name, a short option value) passes through unchanged.
func shortenToken(token string) string {
	if strings.Contains(token, "/") {
		return ShortenPath(token)
	}
	if looksLikeVariableToken(token) {
		return "..."
	}
	return token
}

// pgbrBackupLabelGrammar is pgBackRest's backup-label grammar:
// "YYYYMMDD-HHMMSSF", optionally with one "_YYYYMMDD-HHMMSS[DI]" child
// suffix.
const pgbrBackupLabelGrammar = `\d{8}-\d{6}[FDI](?:_\d{8}-\d{6}[DI])?`

// backupLabelPattern matches a whole pgBackRest backup label.
var backupLabelPattern = regexp.MustCompile(`^` + pgbrBackupLabelGrammar + `$`)

// looksLikeVariableToken reports whether token is the kind of value
// that changes on every occurrence of an otherwise-identical message:
// a WAL segment or timeline name (16 or more uppercase hex chars,
// optionally a ".history" file) or a backup label.
func looksLikeVariableToken(token string) bool {
	seg := strings.TrimSuffix(token, ".history")
	if len(seg) >= 16 && isHexUpper(seg) {
		return true
	}
	return backupLabelPattern.MatchString(token)
}

// --- pgbrStanza message classification ---

func (st *pgbrStanza) mostRecentOpen(command string) *pgbrRun {
	q := st.openRuns[command]
	if len(q) == 0 {
		return nil
	}
	return q[len(q)-1]
}

// pgbrRepoSuffixRe pulls the "repoN" a line's trailing "on repoN"
// names, if any — the signal setSummary uses to tell a genuinely
// multi-repo invocation (two lines about two different repos) from an
// ordinary command whose single-repo summary just gets refined by a
// second info line that happens to mention the same repo again.
var pgbrRepoSuffixRe = regexp.MustCompile(`\bon (repo\d+)\b`)

func pgbrRepoSuffix(s string) string {
	m := pgbrRepoSuffixRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// setSummary records text as cmd's pending one-line Operation.Summary:
// the latest line wins, except that lines naming different repos (one
// per repo of a multi-repo run) are all kept.
func (st *pgbrStanza) setSummary(cmd, text string) {
	if existing, ok := st.pendingSummary[cmd]; ok && existing != text {
		if r1, r2 := pgbrRepoSuffix(existing), pgbrRepoSuffix(text); r1 != "" && r2 != "" && r1 != r2 {
			st.pendingSummary[cmd] = existing + "; " + text
			return
		}
	}
	st.pendingSummary[cmd] = text
}

func (st *pgbrStanza) addIssue(list *[]PgBackRestIssue, idx map[string]int, code int, hasCode bool, firstLine, example, repoLine string, detail []string, ts time.Time, cmd string) {
	norm := normalizePgBackRestMessage(firstLine)
	key := issueKey(code, hasCode, norm, detail)
	if i, ok := idx[key]; ok {
		is := &(*list)[i]
		is.Count++
		is.addTime(ts)
		widenSpan(&is.First, &is.Last, ts)
		if cmd != "" && !slices.Contains(is.Commands, cmd) {
			is.Commands = append(is.Commands, cmd)
		}
		if cmd != "" {
			if is.cmdCount == nil {
				is.cmdCount = map[string]int{}
			}
			is.cmdCount[cmd]++
		}
		return
	}
	is := PgBackRestIssue{Code: code, HasCode: hasCode, Message: norm, Example: example, RepoLine: repoLine, Detail: detail, Count: 1, First: ts, Last: ts}
	is.addTime(ts)
	if cmd != "" {
		is.Commands = []string{cmd}
		is.cmdCount = map[string]int{cmd: 1}
	}
	idx[key] = len(*list)
	*list = append(*list, is)
}

// bumpFailureStretch counts one failure of seg in the stretch of key
// (the segment for archive-push, the data directory for archive-get).
// The synchronous call and the async worker keep separate stretches
// (async), merged at Finalize when they overlap: both see the same outage.
func (st *pgbrStanza) bumpFailureStretch(m map[string]*PgBackRestFailureStretch, key, seg string, async bool, code int, text string, ts time.Time) {
	key = stretchKey(key, async)
	f := m[key]
	if f == nil {
		m[key] = &PgBackRestFailureStretch{Segment: seg, Code: code, FirstLine: text, First: ts, Last: ts, Attempts: 1}
		return
	}
	f.Attempts++
	if code != 0 {
		f.Code = code
	}
	// A WAL segment names the stretch better than a .history probe.
	if isWALSegment(seg) && !isWALSegment(f.Segment) {
		f.Segment = seg
	}
	widenSpan(&f.First, &f.Last, ts)
}

// pgbrAsyncStretchKey suffixes the stretch key of an async worker failure.
const pgbrAsyncStretchKey = "\x00async"

// pgbrWorkingDirRe pulls the data directory out of archive-get's [032]
// "PostgreSQL working directory '...' is not the same as option pg1-path".
var pgbrWorkingDirRe = regexp.MustCompile(`^PostgreSQL working directory '([^']+)' is not the same as option pg1-path`)

// isWALSegment reports whether s is a WAL segment name.
func isWALSegment(s string) bool { return len(s) == 24 && isHexUpper(s) }

// isHistoryFile reports whether s is a timeline history file name.
func isHistoryFile(s string) bool {
	return len(s) == 16 && strings.HasSuffix(s, ".history") && isHexUpper(s[:8])
}

// pgbrHistoryArgRe matches archive-get's "[0000000N.history, ...]" target.
var pgbrHistoryArgRe = regexp.MustCompile(`^\[([0-9A-F]{8}\.history), `)

// pgbrHistoryArg is the .history file an archive-get begin line asks for.
func pgbrHistoryArg(options string) string {
	if m := pgbrHistoryArgRe.FindStringSubmatch(options); m != nil {
		return m[1]
	}
	return ""
}

// stretchKey is the map key of a failure stretch.
func stretchKey(key string, async bool) string {
	if key == "" {
		key = "_"
	}
	if async {
		key += pgbrAsyncStretchKey
	}
	return key
}

// pgbrErrorTypeRe matches a WARN that is only an error type and message
// ("[RepoInvalidError] unable to find a valid repository").
var pgbrErrorTypeRe = regexp.MustCompile(`^\[[A-Za-z]+Error\] `)

// addIssueCommand records cmd on an issue one occurrence of which was
// counted with no command known.
func (st *pgbrStanza) addIssueCommand(list []PgBackRestIssue, idx map[string]int, code int, hasCode bool, firstLine string, detail []string, cmd string) {
	i, ok := idx[issueKey(code, hasCode, normalizePgBackRestMessage(firstLine), detail)]
	if !ok {
		return
	}
	is := &list[i]
	if !slices.Contains(is.Commands, cmd) {
		is.Commands = append(is.Commands, cmd)
	}
	if is.cmdCount == nil {
		is.cmdCount = map[string]int{}
	}
	is.cmdCount[cmd]++
}

// dropIssue takes back one occurrence at ts of an issue addIssue counted.
func (st *pgbrStanza) dropIssue(list *[]PgBackRestIssue, idx *map[string]int, code int, hasCode bool, firstLine string, detail []string, ts time.Time, cmd string) {
	i, ok := (*idx)[issueKey(code, hasCode, normalizePgBackRestMessage(firstLine), detail)]
	if !ok {
		return
	}
	is := &(*list)[i]
	is.Count--
	if k := slices.Index(is.Times, ts); k >= 0 {
		is.Times = slices.Delete(is.Times, k, k+1)
	}
	if cmd != "" && is.cmdCount[cmd] > 0 {
		if is.cmdCount[cmd]--; is.cmdCount[cmd] == 0 {
			is.Commands = slices.DeleteFunc(is.Commands, func(c string) bool { return c == cmd })
		}
	}
	if is.Count <= 0 {
		*list = slices.Delete(*list, i, i+1)
		*idx = rebuildPgBackRestIssueIndex(*list)
		return
	}
	if len(is.Times) > 0 {
		is.First, is.Last = slices.MinFunc(is.Times, time.Time.Compare), slices.MaxFunc(is.Times, time.Time.Compare)
	}
}

// stretchSlice returns the stretches in time order, an async worker's
// stretch merged with the synchronous one it overlaps.
func stretchSlice(m map[string]*PgBackRestFailureStretch) []PgBackRestFailureStretch {
	var syncs, asyncs []PgBackRestFailureStretch
	for key, f := range m {
		if strings.HasSuffix(key, pgbrAsyncStretchKey) {
			asyncs = append(asyncs, *f)
		} else {
			syncs = append(syncs, *f)
		}
	}
	sortStretches(syncs)
	sortStretches(asyncs)
	return mergeFailureStretches(syncs, asyncs)
}

// sortStretches orders stretches by first failure, then segment.
func sortStretches(s []PgBackRestFailureStretch) {
	sort.Slice(s, func(i, j int) bool {
		if !s[i].First.Equal(s[j].First) {
			return s[i].First.Before(s[j].First)
		}
		return s[i].Segment < s[j].Segment
	})
}

// observeArchiveFailure updates archive-push/get failure counters and
// stretches for an ERROR line of that command (async ones apart, see
// PgBackRestArchiveStats). The segment is the open run's, else one the
// text names.
func (st *pgbrStanza) observeArchiveFailure(cmd string, run *pgbrRun, text string, code int, ts time.Time) {
	segment := ""
	if run != nil {
		segment = run.segment
	}
	if segment == "" {
		segment = extractSegmentHint(text)
	}
	async := strings.HasSuffix(cmd, ":async")
	switch baseCommand(cmd) {
	case "archive-push":
		if async {
			st.ArchivePush.FailedAsync++
		} else {
			st.ArchivePush.Failed++
		}
		st.bumpFailureStretch(st.pushFail, segment, segment, async, code, text, ts)
	case "archive-get":
		if async {
			st.ArchiveGet.FailedAsync++
		} else {
			st.ArchiveGet.Failed++
		}
		// Kept per data directory: one recovery's outage, whatever it asked
		// for. A path pgBackRest refused names the directory that ran it.
		path, segment := "", ""
		if run != nil {
			path, segment = run.pg1Path, run.segment
		}
		if m := pgbrWorkingDirRe.FindStringSubmatch(text); m != nil {
			path = m[1]
		}
		key := path
		if key == "" {
			key = segment
		}
		st.bumpFailureStretch(st.getFail, key, segment, async, code, text, ts)
		if f := st.getFail[stretchKey(key, async)]; f != nil {
			f.Path = path
		}
	}
	// ArchiveTimeoutError [082]: a WAL segment did not reach the archive
	// (push) in time, whether the waiter was archive-push itself or a
	// backup/check command blocked on its own "check archive for
	// segment(s)" step — it is always about a push, never a get.
	if code == 82 {
		st.ArchivePush.Timeouts++
	}
}

// observeWarn handles the pgBackRest WARN lines that feed a specific
// counter beyond the generic grouped-warnings list.
func (st *pgbrStanza) observeWarn(text string, ts time.Time) {
	switch {
	case strings.Contains(text, "backup has been changed to full"):
		if run := st.mostRecentOpen("backup"); run != nil {
			run.changedToFull = true
			run.changedFromType = pgbrWordBeforeChangedToFull(text)
		}
	case strings.HasPrefix(text, "dropped WAL file '"):
		seg := extractQuoted(text)
		st.ArchivePush.DroppedEvents = append(st.ArchivePush.DroppedEvents, PgBackRestDropEvent{Segment: seg, Time: ts})
		// The call that drops the segment then logs it as "pushed ...
		// asynchronously": remembered so that line does not count as a
		// push.
		st.lastDroppedSegment = seg
		if run := st.mostRecentOpen("archive-push:async"); run != nil {
			run.pushDropped++
		}
	case strings.HasPrefix(text, "could not push WAL file '"):
		// Only the async worker logs this, "...: [NNN] message".
		seg, code := extractQuoted(text), 0
		if i := strings.Index(text, "): ["); i >= 0 {
			code = leadingInt(text[i+4:])
		}
		st.ArchivePush.FailedAsync++
		st.bumpFailureStretch(st.pushFail, seg, seg, true, code, text, ts)
		if run := st.mostRecentOpen("archive-push:async"); run != nil && seg != "" {
			run.pushFailed = append(run.pushFailed, seg)
		}
	}
}

// observeInfo handles INFO/DETAIL (and DEBUG/TRACE, which fall through
// unmatched) lines that feed backup/archive/expire/operation state.
func (st *pgbrStanza) observeInfo(text string, ts time.Time) {
	switch {
	case strings.HasPrefix(text, "new backup label = "):
		if run := st.mostRecentOpen("backup"); run != nil {
			run.backupLabel = strings.TrimPrefix(text, "new backup label = ")
		}
	case strings.Contains(text, "backup size = ") && strings.Contains(text, "file total = "):
		st.parseBackupSize(text)

	case strings.HasPrefix(text, "push ") && strings.Contains(text, "WAL file(s) to archive: "):
		if seg := extractPushPullSegment(text, "WAL file(s) to archive: "); seg != "" {
			if run := st.mostRecentOpen("archive-push:async"); run != nil {
				run.segment = seg
				run.pushLast = seg
				if i := strings.Index(text, "..."); i >= 0 {
					run.pushLast = segmentOrEmpty(text[i+3:])
				}
				run.pushCount = leadingInt(strings.TrimPrefix(text, "push "))
			} else if run := st.mostRecentOpen("archive-push"); run != nil && run.segment == "" {
				run.segment = seg
			}
		}
	case strings.HasPrefix(text, "get ") && strings.Contains(text, "WAL file(s) from archive: "):
		if seg := extractPushPullSegment(text, "WAL file(s) from archive: "); seg != "" {
			if run := st.mostRecentOpen("archive-get:async"); run != nil {
				run.segment = seg
			} else if run := st.mostRecentOpen("archive-get"); run != nil && run.segment == "" {
				run.segment = seg
			}
		}

	case strings.HasPrefix(text, "pushed WAL file '") && strings.HasSuffix(text, "to the archive asynchronously"):
		if seg := extractQuoted(text); seg != "" && seg == st.lastDroppedSegment {
			// The segment just dropped (see observeWarn): not pushed.
			st.lastDroppedSegment = ""
			break
		}
		st.asyncPushedSegs = append(st.asyncPushedSegs, pgbrAsyncSeg{seg: extractQuoted(text), at: ts})
		st.pushes = append(st.pushes, PgBackRestPush{Segment: extractQuoted(text), Time: ts, Mode: "async"})
	case strings.HasPrefix(text, "pushed WAL file '") && strings.HasSuffix(text, "to the archive"):
		st.ArchivePush.Pushed++
		st.syncPushAt = append(st.syncPushAt, ts)
		st.pushes = append(st.pushes, PgBackRestPush{Segment: extractQuoted(text), Time: ts, Mode: "sync"})

	case strings.HasPrefix(text, "found ") && strings.HasSuffix(text, "in the archive asynchronously"):
		st.ArchiveGet.FoundAsync++
		st.noteGetFound(text, ts)
	case strings.HasPrefix(text, "found ") && strings.Contains(text, "archive"):
		st.ArchiveGet.Found++
		st.noteGetFound(text, ts)
	case strings.HasPrefix(text, "unable to find ") && strings.HasSuffix(text, "in the archive asynchronously"):
		st.ArchiveGet.NotFoundAsync++
		st.noteGetMiss(text, ts)
	case strings.HasPrefix(text, "unable to find ") && strings.Contains(text, "in the archive"):
		st.ArchiveGet.NotFound++
		st.noteGetMiss(text, ts)

	case strings.HasPrefix(text, "stanza-create for stanza '"), strings.Contains(text, "already exists on"):
		st.setSummary("stanza-create", text)
	case strings.HasPrefix(text, "check ") && (strings.Contains(text, "configuration") || strings.Contains(text, "archive for WAL")):
		st.setSummary("check", text)
	case strings.HasPrefix(text, "WAL segment ") && strings.Contains(text, "successfully archived to"):
		st.setSummary("check", text)
	case strings.Contains(text, "restore backup set"):
		st.setSummary("restore", text)
	case strings.HasPrefix(text, "upgrade repository format"), strings.Contains(text, "is already up to date"), strings.HasPrefix(text, "stanza-upgrade for stanza '"):
		st.setSummary("stanza-upgrade", text)
	case strings.HasPrefix(text, "stanza: ") || strings.Contains(text, "status: "):
		st.setSummary("verify", text)

	case strings.HasPrefix(text, "repo") && strings.Contains(text, ": expire "):
		st.observeExpireLine(text)
	case strings.HasPrefix(text, "repo") && strings.Contains(text, "remove expired backup "):
		st.observeExpireRemoved(text)
	case strings.HasPrefix(text, "repo") && strings.Contains(text, "remove archive, start = "):
		if st.curExpire != nil {
			st.curExpire.ArchiveRemoved++
		}
	case strings.HasPrefix(text, "option 'repo") && strings.Contains(text, "-retention-archive' is not set"):
		if st.curExpire != nil {
			st.curExpire.Warnings = append(st.curExpire.Warnings, text)
		}
	}
}

// getPath is the data directory (--pg1-path) of the open archive-get.
func (st *pgbrStanza) getPath() string {
	if run := st.mostRecentOpen("archive-get"); run != nil {
		return run.pg1Path
	}
	return ""
}

// noteGetFound records a segment archive-get found, for LastFound and
// the recovery of its data directory's stretches.
func (st *pgbrStanza) noteGetFound(text string, ts time.Time) {
	if ts.After(st.ArchiveGet.LastFound) {
		st.ArchiveGet.LastFound = ts
	}
	path := st.getPath()
	if ts.After(st.getFoundAt[path]) {
		st.getFoundAt[path] = ts
	}
	if !ts.IsZero() {
		st.getFoundTimes[path] = append(st.getFoundTimes[path], ts)
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(text, "found "), " ")
	if k := path + "\x00" + seg; ts.After(st.getFoundSeg[k]) {
		st.getFoundSeg[k] = ts
	}
}

// noteGetMiss records an "unable to find FILE in the archive" of a WAL
// segment or a .history file, for missingWAL: a probe for a file never
// pushed is no loss, one pushed before is.
func (st *pgbrStanza) noteGetMiss(text string, ts time.Time) {
	seg, _, _ := strings.Cut(strings.TrimPrefix(text, "unable to find "), " ")
	if isWALSegment(seg) || isHistoryFile(seg) {
		st.getMisses = append(st.getMisses, PgBackRestMissingWAL{Segment: seg, Path: st.getPath(), Time: ts})
	}
}

// pgbrSizeUnits are pgBackRest's size units (strSizeFormat), 1024-based.
var pgbrSizeUnits = map[string]float64{
	"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40, "PB": 1 << 50,
}

// parsePgBackRestSize converts a size as pgBackRest writes it ("29.4MB",
// "8.3KB", "512B") to bytes.
func parsePgBackRestSize(raw string) (int64, bool) {
	i := 0
	for i < len(raw) && (raw[i] == '.' || isDigit(raw[i])) {
		i++
	}
	mult, ok := pgbrSizeUnits[strings.TrimSpace(raw[i:])]
	if i == 0 || !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw[:i], 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(v * mult)), true
}

func (st *pgbrStanza) parseBackupSize(text string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	typ := fields[0]
	run := st.mostRecentOpen("backup")
	if run == nil {
		return
	}
	if typ == "full" || typ == "diff" || typ == "incr" {
		run.sizeType = typ
	}
	if i := strings.Index(text, "size = "); i >= 0 {
		rest := text[i+len("size = "):]
		if c := strings.IndexByte(rest, ','); c >= 0 {
			rest = rest[:c]
		}
		run.sizeRaw = rest
	}
	if i := strings.Index(text, "file total = "); i >= 0 {
		rest := strings.TrimSpace(text[i+len("file total = "):])
		if v, err := strconv.Atoi(rest); err == nil {
			run.fileTotal = v
		}
	}
}

// observeExpireLine records which repo an expire run targeted, unless
// it covers every repository. The labels come from the "remove expired
// backup X" lines (observeExpireRemoved): "repo1: expire full backup set
// A, B, C" mixes a full backup and its children.
func (st *pgbrStanza) observeExpireLine(text string) {
	if st.curExpire == nil || st.curExpire.Repo == PgBackRestAllRepos {
		return
	}
	idx := strings.Index(text, ": expire ")
	if idx < 0 {
		return
	}
	st.curExpire.Repo = text[:idx]
}

// pgbrWordBeforeChangedToFull extracts the backup type word from
// pgBackRest's own "<word> backup has been changed to full" WARN (e.g.
// "no prior backup exists, incr backup has been changed to full") —
// the only place that type is stated when the run's --type= option was
// never given (pgBackRest's own default is implicit otherwise, and
// would be lost once Type is normalized to "full"). Returns "" when
// the marker is not found.
func pgbrWordBeforeChangedToFull(text string) string {
	const marker = " backup has been changed to full"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	start := idx
	for start > 0 && text[start-1] != ' ' && text[start-1] != ',' {
		start--
	}
	return text[start:idx]
}

// applyRunType sets b.Type the way pgBackRest decides it: the type the
// "backup size" line names, else --type=, else the label's suffix, else
// incr, pgBackRest's default. When the "changed to full" WARN fired, Type
// is full and RequestedType keeps the request.
func applyRunType(b *PgBackRestBackup, run *pgbrRun) {
	switch {
	case run.sizeType != "":
		b.Type = run.sizeType
	case run.typeOpt != "":
		b.Type = run.typeOpt
	case labelType(run.backupLabel) != "":
		b.Type = labelType(run.backupLabel)
	default:
		b.Type = "incr"
	}
	if run.changedToFull {
		if run.typeOpt != "" {
			b.RequestedType = run.typeOpt
		} else {
			b.RequestedType = run.changedFromType
		}
		b.Type = "full"
		b.ChangedToFull = true
	}
}

// pgbrCheckpointWait names the checkpoint a backup's "backup begins after
// the ... checkpoint completes" line says it waits for, "" for any other
// line.
func pgbrCheckpointWait(text string) string {
	const marker = "backup begins after the "
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	what, ok := strings.CutSuffix(text[i+len(marker):], " completes")
	if !ok || !strings.HasSuffix(what, "checkpoint") {
		return ""
	}
	return what
}

// labelType reads the backup type from a label's last letter (F, D, I).
func labelType(label string) string {
	if label == "" {
		return ""
	}
	switch label[len(label)-1] {
	case 'F':
		return "full"
	case 'D':
		return "diff"
	case 'I':
		return "incr"
	}
	return ""
}

// runEnd is how a run ended: its "command end" line, or none (crashed
// or still running past the --end bound).
type runEnd struct {
	time       time.Time
	success    bool
	dur        time.Duration
	hasDur     bool
	errCode    int
	hasErrCode bool
	// crashed: no "command end", the process was killed or crashed.
	crashed bool
	// inProgress: no "command end" before the window's end, while lines
	// after it were left out.
	inProgress bool
}

// finishRun records a run whose command begin was seen: a Backup entry
// for backup, an Operation entry for every other command (expire is
// handled separately by its curExpire accumulation), and the command's
// run/succeeded/failed/duration rollup in every case.
func (st *pgbrStanza) finishRun(cmd string, run *pgbrRun, end runEnd) {
	// A ":local"/":remote" command is a protocol worker of the real
	// command (log-subprocess=y), not a run of its own.
	if run == nil || strings.HasSuffix(cmd, ":local") || strings.HasSuffix(cmd, ":remote") {
		return
	}
	cs := st.commandStat(cmd)
	cs.Runs++
	switch {
	case end.success:
		cs.Succeeded++
	case !end.inProgress:
		cs.Failed++
	}
	// pgBackRest writes "(Nms)" only on success: a failed run's duration
	// comes from its begin and end lines.
	if !end.hasDur && !end.crashed && !end.inProgress && !run.start.IsZero() && !end.time.Before(run.start) {
		end.dur, end.hasDur = end.time.Sub(run.start), true
	}
	if end.hasDur {
		cs.TotalDuration += end.dur
		cs.MaxDuration = max(cs.MaxDuration, end.dur)
	}
	signal := ""
	if end.crashed {
		signal = run.signal
	}

	base := baseCommand(cmd)
	switch base {
	case "backup":
		b := PgBackRestBackup{Start: run.start, End: end.time, Succeeded: end.success, Duration: end.dur, HasDuration: end.hasDur,
			Repo: run.repo, Label: run.backupLabel, ExecID: run.execID, SizeRaw: run.sizeRaw, FileTotal: run.fileTotal,
			Crashed: end.crashed, InProgress: end.inProgress, Signal: signal}
		b.SizeBytes, b.HasSize = parsePgBackRestSize(run.sizeRaw)
		if end.hasErrCode {
			b.ErrorCode = end.errCode
		}
		applyRunType(&b, run)
		if end.crashed || end.inProgress {
			b.End, b.LastLine = time.Time{}, end.time
		}
		if end.crashed {
			b.WaitingFor = pgbrCheckpointWait(run.lastText)
		}
		if !end.success && !end.crashed && !end.inProgress {
			b.ErrorLine, b.ErrorDetail = st.lastErrorFirstLine, st.lastErrorDetail
		}
		st.Backups = append(st.Backups, b)
	case "expire":
		// Finalized by the caller once curExpire's command end fires; what
		// a killed expire removed is unknown, and the rollup counts it.
	case "archive-push", "archive-get":
		// Counted line by line in ArchivePush/ArchiveGet, but an async
		// worker logs its pushes only as a batch.
		if cmd == "archive-push:async" && end.success && run.pushCount > 0 {
			n := run.pushCount - run.pushDropped - len(run.pushFailed)
			st.asyncPushRanges = append(st.asyncPushRanges, pgbrPushRange{first: run.segment, last: run.pushLast, n: n, at: end.time})
			if n > 0 {
				st.pushes = append(st.pushes, PgBackRestPush{Segment: run.lastPushedSegment(), Time: end.time, Mode: "async", batchFailed: run.pushFailed})
			}
		}
	default:
		op := PgBackRestOperation{Command: base, Time: end.time, Succeeded: end.success, HasOutcome: !end.inProgress,
			Duration: end.dur, HasDuration: end.hasDur, Crashed: end.crashed, Signal: signal}
		if end.hasErrCode {
			op.ErrorCode = end.errCode
		}
		if s, ok := st.pendingSummary[base]; ok && !end.crashed && !end.inProgress {
			op.Summary = s
			delete(st.pendingSummary, base)
		}
		if base == "restore" {
			op.Summary = appendRestoreDetail(op.Summary, run)
			op.Path = run.pg1Path
		}
		st.Operations = append(st.Operations, op)
	}
}

// appendRestoreDetail appends the destination (--pg1-path) and recovery
// target (--type=/--target=, "default" when neither was given) to a
// restore's summary, which otherwise names only the backup set.
func appendRestoreDetail(summary string, run *pgbrRun) string {
	target := run.typeOpt
	if target == "" {
		target = "default"
	}
	if run.target != "" {
		target += "=" + run.target
	}
	detail := "target: " + target
	if run.pg1Path != "" {
		detail = "destination: " + run.pg1Path + ", " + detail
	}
	if summary == "" {
		return detail
	}
	return summary + "; " + detail
}

func (st *pgbrStanza) observeExpireRemoved(text string) {
	if st.curExpire == nil {
		return
	}
	idx := strings.Index(text, "remove expired backup ")
	if idx < 0 {
		return
	}
	label := strings.TrimSpace(text[idx+len("remove expired backup "):])
	st.curExpire.Expired = append(st.curExpire.Expired, label)
}

// --- PostgreSQL-side archive failures ---

// mayBeArchiverLine is the byte gate before the prefixes
// observePGArchiveFailure looks for, all lowercase: "archive command
// failed", "archive command was terminated", "archiving write-ahead", "archived write-ahead",
// "could not restore file".
func mayBeArchiverLine(e *parser.LogEntry) bool {
	b := int(e.BodyOffset)
	return b < len(e.Message) && (e.Message[b] == 'a' || e.Message[b] == 'c')
}

// observePGArchiveFailure watches ordinary PostgreSQL entries for the
// archiver's wording: archive_command failures and retry warnings,
// restore_command failures, and the DEBUG1 "archived" line of a
// successful archive (a push). A failure counts for the stanza of the
// pgbackrest archive_command its DETAIL shows, not at all when it shows
// another program. Without a DETAIL, it follows the last one seen; with
// none seen, it waits, like the retry warnings and restore_command
// failures, for pgBackRest to show elsewhere (see Finalize).
func (a *PgBackRestAnalyzer) observePGArchiveFailure(e *parser.LogEntry) {
	if a.pendingFail != nil {
		a.tryCompletePending(e)
	}
	if !mayBeArchiverLine(e) {
		return
	}
	body := e.Message[e.BodyOffset:]
	// On pgBackRest's wall clock, like every time of the analyzer.
	ts := pgbrWall(e.Timestamp)
	switch {
	case strings.HasPrefix(body, parser.ArchiveFailedExitPrefix):
		a.pendingFail = &pgbrPendingPGFailure{ts: ts, hasExit: true, exitCode: leadingInt(body[len(parser.ArchiveFailedExitPrefix):])}
	case strings.HasPrefix(body, parser.ArchiveFailedSignalPrefix):
		a.pendingFail = &pgbrPendingPGFailure{ts: ts, isSignal: true, signal: leadingInt(body[len(parser.ArchiveFailedSignalPrefix):])}
	case strings.HasPrefix(body, "archiving write-ahead log file \""):
		switch a.archiveCmd {
		case 1:
			st := a.stanzaFor("")
			st.noteGlobalBounds(ts)
			st.PGRetryWarnings++
			st.PGRetryTimes = append(st.PGRetryTimes, ts)
			st.hasContent = true
		case 0:
			st := a.stanzaFor("")
			st.deferredRetries = append(st.deferredRetries, ts)
		}
	case strings.HasPrefix(body, "archived write-ahead log file \""):
		p := PgBackRestPush{Segment: extractDoubleQuoted(body), Time: ts, Mode: "postgresql"}
		switch a.archiveCmd {
		case 1:
			st := a.stanzaFor("")
			st.pushes = append(st.pushes, p)
		case 0:
			st := a.stanzaFor("")
			st.deferredArchived = append(st.deferredArchived, p)
		}
	case strings.HasPrefix(body, "could not restore file \""):
		st := a.stanzaFor("")
		st.deferredRestores = append(st.deferredRestores, ts)
	}
}

func (a *PgBackRestAnalyzer) tryCompletePending(e *parser.LogEntry) {
	p := a.pendingFail
	a.pendingFail = nil
	cmdline := ""
	if e.IsContinuation {
		if idx := strings.Index(e.Message, parser.FailedArchiveCommandPrefix); idx >= 0 {
			cmdline = e.Message[idx+len(parser.FailedArchiveCommandPrefix):]
		}
	}
	f := PgBackRestPGArchiveFailure{Time: p.ts, ExitCode: p.exitCode, HasExit: p.hasExit, Signal: p.signal, IsSignal: p.isSignal}
	if cmdline == "" {
		switch a.archiveCmd {
		case 1:
			a.recordPGFailure("", f)
		case 0:
			st := a.stanzaFor("")
			st.deferredFailures = append(st.deferredFailures, f)
		}
		return
	}
	if !parser.ArchiveCommandIsPgBackRest(cmdline) {
		a.archiveCmd = -1
		return
	}
	a.archiveCmd = 1
	a.evidence = true
	stanza, _ := parseOption(cmdline, "stanza")
	f.Segment = extractArchiveSegmentArg(cmdline)
	a.recordPGFailure(stanza, f)
}

// recordPGFailure files a PostgreSQL archive failure under stanza.
func (a *PgBackRestAnalyzer) recordPGFailure(stanza string, f PgBackRestPGArchiveFailure) {
	st := a.stanzaFor(stanza)
	st.noteGlobalBounds(f.Time)
	st.PGArchiveFailures = append(st.PGArchiveFailures, f)
	st.hasContent = true
}

func extractArchiveSegmentArg(cmdline string) string {
	// The last word naming a WAL file: a raw line PostgreSQL appended to
	// the DETAIL (the shell's "sh: 1: pgbackrest: not found") is not one.
	fields := strings.Fields(cmdline)
	for i := len(fields) - 1; i >= 0; i-- {
		f := strings.Trim(fields[i], "\"")
		if j := strings.LastIndexByte(f, '/'); j >= 0 {
			f = f[j+1:]
		}
		if seg := segmentOrEmpty(f); seg != "" {
			return seg
		}
	}
	return ""
}
