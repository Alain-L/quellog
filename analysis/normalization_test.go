package analysis

import (
	"fmt"
	"testing"
)

func TestNormalizeEventInternal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"ERROR:  relation \"users\" does not exist at character 15",
			"relation ? does not exist at character ?",
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
			"syntax error at or near ? at character ?",
		},
	}

	for _, tt := range tests {
		got := NormalizeEvent(tt.input)
		if got != tt.expected {
			fmt.Printf("FAIL: input=%q, got=%q, expected=%q\n", tt.input, got, tt.expected)
		} else {
			fmt.Printf("PASS: %q -> %q\n", tt.input, got)
		}
	}
}
