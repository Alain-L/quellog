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

// computeSingleQueryDurationDistribution calculates a histogram of query duration distribution
// for a specific query ID (similar to the global duration histogram).
//
// Returns:
//   - histogram: map of duration ranges to query count
//   - unit: "queries"
//   - scaleFactor: for proportional display
func computeSingleQueryDurationDistribution(executions []analysis.QueryExecution, queryID string) (map[string]int, string, int, []string) {
	// Filter executions for this query
	var filtered []analysis.QueryExecution
	for _, exec := range executions {
		if exec.QueryID == queryID {
			filtered = append(filtered, exec)
		}
	}

	if len(filtered) == 0 {
		return nil, "", 0, nil
	}

	// Define duration buckets (same as global report)
	bucketDefinitions := []struct {
		label string
		lower float64 // Lower bound in ms (inclusive)
		upper float64 // Upper bound in ms (exclusive)
	}{
		{"< 1 ms", 0, 1},
		{"< 10 ms", 1, 10},
		{"< 100 ms", 10, 100},
		{"< 1 s", 100, 1000},
		{"< 10 s", 1000, 10000},
		{">= 10 s", 10000, math.Inf(1)},
	}

	// Initialize histogram
	histogram := make(map[string]int)
	orderedLabels := make([]string, 0, len(bucketDefinitions))
	for _, bucket := range bucketDefinitions {
		histogram[bucket.label] = 0
		orderedLabels = append(orderedLabels, bucket.label)
	}

	// Distribute executions into buckets
	for _, exec := range filtered {
		d := exec.Duration
		for _, bucket := range bucketDefinitions {
			if d >= bucket.lower && d < bucket.upper {
				histogram[bucket.label]++
				break
			}
		}
	}

	// Calculate scale factor
	maxCount := 0
	for _, count := range histogram {
		if count > maxCount {
			maxCount = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxCount) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, "queries", scaleFactor, orderedLabels
}

// computeSingleQueryTempFileCountHistogram calculates a histogram of temp file count
// over time for a specific query ID.
//
// Returns:
//   - histogram: map of time range labels to temp file count
//   - unit: "files"
//   - scaleFactor: for proportional display
func computeSingleQueryTempFileCountHistogram(events []analysis.TempFileEvent, queryID string) (map[string]int, string, int) {
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

// computeSingleQueryTempFileHistogram calculates a histogram of temp file sizes
// over time for a specific query ID.
//
// Returns:
//   - histogram: map of time range labels to cumulative temp file size
//   - unit: "MB" or "GB" depending on scale
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

	// Prepare buckets (store sizes in bytes)
	histogramSizes := make([]int64, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := start.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
	}

	// Sum temp file sizes per bucket
	for _, event := range filtered {
		elapsed := event.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogramSizes[bucketIndex] += int64(event.Size)
	}

	// Determine unit and conversion based on max bucket size
	maxBucketSize := int64(0)
	for _, size := range histogramSizes {
		if size > maxBucketSize {
			maxBucketSize = size
		}
	}

	var unit string
	var conversion int64
	if maxBucketSize < 1024*1024*1024 {
		unit = "MB"
		conversion = 1024 * 1024
	} else {
		unit = "GB"
		conversion = 1024 * 1024 * 1024
	}

	// Convert to map with unit conversion
	result := make(map[string]int, numBuckets)
	for i, raw := range histogramSizes {
		var value int
		if raw > 0 {
			value = int((raw + conversion - 1) / conversion)
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
