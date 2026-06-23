package analysis

import (
	"testing"
)

func TestNormalizeEventInternal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"ERROR:  relation \"users\" does not exist at character 15",
			"relation ? does not exist",
		},
		{
			"WARNING:  out of shared memory",
			"out of shared memory",
		},
		{
			"LOG:  database system is ready to accept connections",
			"database system is ready to accept connections",
		},
		{
			"ERROR:  duplicate key value violates unique constraint \"users_pkey\"",
			"duplicate key value violates unique constraint ?",
		},
		{
			"ERROR:  syntax error at or near \"FROM\" at character 8",
			"syntax error at or near ?",
		},
	}

	for _, tt := range tests {
		got := NormalizeEvent(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeEvent(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestCollapseInLists_ParityWithRegex pins the byte-level collapse
// against the original inListRegex on every shape that matters: the
// word boundary ("join (?"), single and many placeholders, multiline
// whitespace inside the list, trailing whitespace before ')', broken
// lists that must NOT collapse, multiple lists per query, and lists at
// string boundaries.
func TestCollapseInLists_ParityWithRegex(t *testing.T) {
	cases := []string{
		"select * from t where id in (?)",
		"select * from t where id in (?, ?, ?)",
		"select * from t where id in (?,?,?)",
		"select * from t where id in (? , ? , ?)",
		"select * from t where id in (?, ?,\n\t?, ?)",
		"select * from t where id in (?, ? )",
		"update t set x = ? where id in (?, ?) and y in (?, ?, ?)",
		"select * from t join (?) s on true",         // word boundary: no collapse
		"select * from t where id in (?, 5)",         // literal inside: no collapse
		"select * from t where id in (",              // unterminated
		"select * from t where id in (?",             // unterminated after ?
		"select * from t where id in (?, ",           // unterminated after comma
		"in (?)",                                     // at string start
		"where a in (?) and b in (?, ?) or c in (?)", // several lists
		"select * from t where id in (?; ?)",         // bad separator: no collapse
		"select * from t where x in (??)",            // double ? : no collapse
		"margin (?)",                                 // identifier prefix: no collapse
	}
	for _, c := range cases {
		want := inListRegex.ReplaceAllString(c, "in (...)")
		got := collapseInLists(c)
		if got != want {
			t.Errorf("collapseInLists(%q)\n  got  %q\n  want %q", c, got, want)
		}
	}
}

// TestReplPrefilter_LongPrefix pins the prefilter scan cap against the
// real-world shape that broke a 128-byte cap: a rich log_line_prefix
// (db=…,user=…,app=…,client=…) pushing the marker past byte 150.
func TestReplPrefilter_LongPrefix(t *testing.T) {
	longPrefix := "db=prf_germinal_v15,user=prf_germinal_v15_lantier,app=pg_1556260_sync_1547474_7600395482884909846,client=10.99.0.1(34102), LOG:  00000: "
	cases := []struct {
		msg  string
		want bool
	}{
		{longPrefix + "terminating walsender process due to replication timeout", true},
		{longPrefix + "started streaming WAL from primary at 0/3000000", true},
		{longPrefix + "duration: 0.082 ms  statement: DISCARD ALL", false},
		{"terminating walsender process due to replication timeout", true},
	}
	for _, c := range cases {
		if got := replPrefilter(c.msg); got != c.want {
			t.Errorf("replPrefilter(%.60q…) = %v, want %v", c.msg, got, c.want)
		}
	}
}
