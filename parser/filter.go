package parser

import (
	"strings"
	"time"
)

// LogFilters ...
type LogFilters struct {
	BeginT      time.Time
	EndT        time.Time
	DbFilter    []string
	UserFilter  []string
	ExcludeUser []string
	AppFilter   []string
	GrepExpr    []string
}

// FilterStream lit in, applique les filtres, et envoie les logs validés à out.
func FilterStream(in <-chan LogEntry, out chan<- LogEntry, filters LogFilters) {
	defer close(out)

	for e := range in {
		if !filters.BeginT.IsZero() && e.Timestamp.Before(filters.BeginT) {
			continue
		}
		if !filters.EndT.IsZero() && e.Timestamp.After(filters.EndT) {
			continue
		}

		if len(filters.DbFilter) > 0 {
			dbName, _ := extractKeyValue(e.Message, "db=")
			if dbName == "" || !sliceContains(filters.DbFilter, dbName) {
				continue
			}
		}

		if len(filters.UserFilter) > 0 {
			uName, _ := extractKeyValue(e.Message, "user=")
			if uName == "" || !sliceContains(filters.UserFilter, uName) {
				continue
			}
		}

		if len(filters.ExcludeUser) > 0 {
			uName, _ := extractKeyValue(e.Message, "user=")
			if sliceContains(filters.ExcludeUser, uName) {
				continue
			}
		}

		if len(filters.AppFilter) > 0 {
			appName, _ := extractKeyValue(e.Message, "app=")
			if appName == "" || !sliceContains(filters.AppFilter, appName) {
				continue
			}
		}

		// grep logic => containAll ?
		if len(filters.GrepExpr) > 0 {
			if !containsAll(e.Message, filters.GrepExpr) {
				continue
			}
		}

		out <- e
	}
}

// containsAll : toutes les chaînes doivent être présentes
func containsAll(s string, patterns []string) bool {
	for _, p := range patterns {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func extractKeyValue(line, key string) (string, bool) {
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len(key):]
	seps := []rune{' ', ',', '[', ')'}
	minPos := len(rest)
	for _, c := range seps {
		if pos := strings.IndexRune(rest, c); pos != -1 && pos < minPos {
			minPos = pos
		}
	}
	val := strings.TrimSpace(rest[:minPos])
	if val == "" {
		return "", false
	}
	return val, true
}

func sliceContains(arr []string, s string) bool {
	for _, a := range arr {
		if a == s {
			return true
		}
	}
	return false
}
