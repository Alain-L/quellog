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
