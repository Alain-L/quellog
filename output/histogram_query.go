// Package output provides formatting and display functions for analysis results.
package output

import (
	"fmt"
	"math"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// computeSingleQueryExecutionHistogram calculates a histogram of execution count over time
// for a specific query ID.
//
// Returns:
//   - histogram: map of time range labels to execution count
//   - unit: "executions"
//   - scaleFactor: for proportional display
func computeSingleQueryExecutionHistogram(executions []analysis.QueryExecution, queryID string) (map[string]int, string, int) {
	// Filter executions for this query
	var filtered []analysis.QueryExecution
	for _, exec := range executions {
		if exec.QueryID == queryID {
			filtered = append(filtered, exec)
		}
	}

	if len(filtered) == 0 {
		return nil, "", 0
	}

	// Find time range
	start := filtered[0].Timestamp
	end := filtered[0].Timestamp
	for _, exec := range filtered {
		if exec.Timestamp.Before(start) {
			start = exec.Timestamp
		}
		if exec.Timestamp.After(end) {
			end = exec.Timestamp
		}
	}

	// Divide into 6 buckets
	totalDuration := end.Sub(start)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Prepare buckets
	histogram := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := start.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
	}

	// Count executions per bucket
	for _, exec := range filtered {
		elapsed := exec.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogram[bucketIndex]++
	}

	// Convert to map
	result := make(map[string]int, numBuckets)
	for i, count := range histogram {
		result[bucketLabels[i]] = count
	}

	// Calculate scale factor
	maxValue := 0
	for _, count := range histogram {
		if count > maxValue {
			maxValue = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return result, "x", scaleFactor
}

// computeSingleQueryTimeHistogram calculates a histogram of cumulative execution time
// over time for a specific query ID.
//
// Returns:
//   - histogram: map of time range labels to cumulative time
//   - unit: "ms", "s", or "m" depending on scale
//   - scaleFactor: for proportional display
func computeSingleQueryTimeHistogram(executions []analysis.QueryExecution, queryID string) (map[string]int, string, int) {
	// Filter executions for this query
	var filtered []analysis.QueryExecution
	for _, exec := range executions {
		if exec.QueryID == queryID {
			filtered = append(filtered, exec)
		}
	}

	if len(filtered) == 0 {
		return nil, "", 0
	}

	// Find time range
	start := filtered[0].Timestamp
	end := filtered[0].Timestamp
	for _, exec := range filtered {
		if exec.Timestamp.Before(start) {
			start = exec.Timestamp
		}
		if exec.Timestamp.After(end) {
			end = exec.Timestamp
		}
	}

	// Divide into 6 buckets
	totalDuration := end.Sub(start)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Prepare buckets
	histogramMs := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := start.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
	}

	// Sum durations per bucket
	for _, exec := range filtered {
		elapsed := exec.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogramMs[bucketIndex] += int(exec.Duration)
	}

	// Determine unit and conversion
	maxBucketLoad := 0
	for _, load := range histogramMs {
		if load > maxBucketLoad {
			maxBucketLoad = load
		}
	}

	var unit string
	var conversion int
	if maxBucketLoad < 1000 {
		unit = "ms"
		conversion = 1
	} else if maxBucketLoad < 60000 {
		unit = "s"
		conversion = 1000
	} else {
		unit = "m"
		conversion = 60000
	}

	// Convert to map with unit conversion
	result := make(map[string]int, numBuckets)
	for i, ms := range histogramMs {
		var value int
		if ms > 0 {
			value = int((ms + conversion - 1) / conversion)
		} else {
			value = 0
		}
		result[bucketLabels[i]] = value
	}

	// Calculate scale factor
	maxValue := 0
	for _, v := range result {
		if v > maxValue {
			maxValue = v
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return result, unit, scaleFactor
}

// computeSingleQueryTempFileHistogram calculates a histogram of temp file count
// over time for a specific query ID.
//
// Returns:
//   - histogram: map of time range labels to temp file count
//   - unit: "files"
//   - scaleFactor: for proportional display
func computeSingleQueryTempFileHistogram(events []analysis.TempFileEvent, queryID string) (map[string]int, string, int) {
	// Filter events for this query
	var filtered []analysis.TempFileEvent
	for _, event := range events {
		if event.QueryID == queryID {
			filtered = append(filtered, event)
		}
	}

	if len(filtered) == 0 {
		return nil, "", 0
	}

	// Find time range
	start := filtered[0].Timestamp
	end := filtered[0].Timestamp
	for _, event := range filtered {
		if event.Timestamp.Before(start) {
			start = event.Timestamp
		}
		if event.Timestamp.After(end) {
			end = event.Timestamp
		}
	}

	// Divide into 6 buckets
	totalDuration := end.Sub(start)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Prepare buckets
	histogram := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := start.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
	}

	// Count temp files per bucket
	for _, event := range filtered {
		elapsed := event.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogram[bucketIndex]++
	}

	// Convert to map
	result := make(map[string]int, numBuckets)
	for i, count := range histogram {
		result[bucketLabels[i]] = count
	}

	// Calculate scale factor
	maxValue := 0
	for _, count := range histogram {
		if count > maxValue {
			maxValue = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return result, "files", scaleFactor
}
