// Package output provides query ranking helpers shared across renderers.
package output

import (
	"sort"

	"github.com/Alain-L/quellog/analysis"
)

// rankedQuery is a flattened, rankable view of a query's stats: the ID,
// the normalized text and the three sortable metrics. Shared by the JSON
// and markdown SQL-performance renderers, which both rank by a metric
// descending and tie-break on the ID. (The text renderer ranks QueryRow
// with a different, query-string tie-break, so it keeps its own path.)
//
// This file carries no build tag so the helpers are available to both the
// CLI renderers (query_table.go, markdown.go — //go:build !js) and the
// WASM JSON export (json.go — no tag).
type rankedQuery struct {
	ID        string
	Query     string
	Count     int
	TotalTime float64
	AvgTime   float64
	MaxTime   float64
}

// flattenQueryStats projects a QueryStats map into a rankable slice.
func flattenQueryStats(stats map[string]*analysis.QueryStat) []rankedQuery {
	out := make([]rankedQuery, 0, len(stats))
	for _, s := range stats {
		out = append(out, rankedQuery{
			ID:        s.ID,
			Query:     s.NormalizedQuery,
			Count:     s.Count,
			TotalTime: s.TotalTime,
			AvgTime:   s.AvgTime,
			MaxTime:   s.MaxTime,
		})
	}
	return out
}

// topRankedQueries sorts rows in place by less and returns the first limit
// entries (or all of them when fewer). rows is mutated, so callers must
// consume the result before the next ranking pass on the same slice.
func topRankedQueries(rows []rankedQuery, less func(a, b rankedQuery) bool, limit int) []rankedQuery {
	sort.Slice(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	if limit > len(rows) {
		limit = len(rows)
	}
	return rows[:limit]
}

// The three metric comparators shared by the JSON and markdown rankings,
// each tie-broken on the stable ID for a deterministic total order.
func rankByMaxTime(a, b rankedQuery) bool {
	if a.MaxTime != b.MaxTime {
		return a.MaxTime > b.MaxTime
	}
	return a.ID < b.ID
}

func rankByCount(a, b rankedQuery) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.ID < b.ID
}

func rankByTotalTime(a, b rankedQuery) bool {
	if a.TotalTime != b.TotalTime {
		return a.TotalTime > b.TotalTime
	}
	return a.ID < b.ID
}
