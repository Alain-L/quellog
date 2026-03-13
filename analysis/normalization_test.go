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
