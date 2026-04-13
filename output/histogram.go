// Package output provides formatting and display functions for analysis results.
package output

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// computeQueryLoadHistogram calculates a histogram of query load over time.
// It divides the time range into 6 equal buckets and sums query durations
// in each bucket.
//
// Returns:
//   - histogram: map of time range labels to total query time
//   - unit: "ms", "s", or "m" depending on the scale
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeQueryLoadHistogram(m analysis.SQLMetrics) (map[string]int, string, int) {
	if m.StartTimestamp.IsZero() || m.EndTimestamp.IsZero() || len(m.Executions) == 0 {
		return nil, "", 0
	}

	// Divide the total interval into 6 equal buckets.
	totalDuration := m.EndTimestamp.Sub(m.StartTimestamp)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Guard against division by zero when total duration is zero or very short.
	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Prepare buckets with millisecond accumulation.
	histogramMs := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		start := m.StartTimestamp.Add(time.Duration(i) * bucketDuration)
		end := m.StartTimestamp.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", start.Format("15:04"), end.Format("15:04"))
	}

	// Distribute durations (in ms) into buckets based on each execution's timestamp.
	for _, exec := range m.Executions {
		elapsed := exec.Timestamp.Sub(m.StartTimestamp)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogramMs[bucketIndex] += int(exec.Duration)
	}

	// Determine the unit and conversion factor based on the maximum bucket load.
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

	// Convert and round up so any non-zero load displays at least 1 unit.
	histogram := make(map[string]int, numBuckets)
	for i, load := range histogramMs {
		var value int
		if load > 0 {
			value = (load + conversion - 1) / conversion
		} else {
			value = 0
		}
		histogram[bucketLabels[i]] = value
	}

	// Compute scale factor for display.
	maxValue := 0
	for _, v := range histogram {
		if v > maxValue {
			maxValue = v
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

// computeQueryDurationHistogram calculates a histogram of query durations.
// It groups queries into fixed duration buckets (< 1ms, < 10ms, etc.).
//
// Returns:
//   - histogram: map of duration labels to query count
//   - unit: "req" (number of requests/queries)
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeQueryDurationHistogram(m analysis.SQLMetrics) (map[string]int, string, int) {
	// Fixed bucket definitions in display order.
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

	// Initialize histogram with zero values to guarantee display order.
	histogram := make(map[string]int)
	for _, bucket := range bucketDefinitions {
		histogram[bucket.label] = 0
	}

	// Distribute queries into buckets.
	for _, exec := range m.Executions {
		d := exec.Duration
		for _, bucket := range bucketDefinitions {
			if d >= bucket.lower && d < bucket.upper {
				histogram[bucket.label]++
				break
			}
		}
	}

	// Find the maximum query count in any bucket.
	maxCount := 0
	for _, count := range histogram {
		if count > maxCount {
			maxCount = count
		}
	}

	// Unit is "req" (number of requests).
	unit := "req"

	// Compute scale factor to limit the longest bar to 40 characters.
	scaleFactor := int(math.Ceil(float64(maxCount) / 40.0))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

// computeTempFileHistogram calculates a histogram of temporary file usage over time.
// It divides the time range into 6 equal buckets and sums file sizes in each bucket.
//
// Returns:
//   - histogram: map of time range labels to total file size
//   - unit: "B", "KB", "MB", or "GB" depending on the scale
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeTempFileHistogram(m analysis.TempFileMetrics) (map[string]int, string, int) {
	// No events means no histogram to build.
	if len(m.Events) == 0 {
		return nil, "", 0
	}

	// Determine the time range from events.
	start := m.Events[0].Timestamp
	end := m.Events[0].Timestamp
	for _, event := range m.Events {
		if event.Timestamp.Before(start) {
			start = event.Timestamp
		}
		if event.Timestamp.After(end) {
			end = event.Timestamp
		}
	}
	totalDuration := end.Sub(start)

	// If all events share the same (or very close) timestamp,
	// a temporal histogram is meaningless — collapse into one bucket.
	if totalDuration < time.Second {
		totalDuration = time.Second
	}

	// Divide the interval into 6 equal buckets.
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Prepare buckets.
	histogramSizes := make([]float64, numBuckets) // cumulative byte sizes per bucket
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogramSizes[i] = 0
	}

	// Distribute events into buckets.
	for _, event := range m.Events {
		elapsed := event.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogramSizes[bucketIndex] += event.Size
	}

	// Find the maximum bucket load.
	maxBucketLoad := 0.0
	for _, size := range histogramSizes {
		if size > maxBucketLoad {
			maxBucketLoad = size
		}
	}

	// Choose the unit and conversion factor based on the maximum bucket load.
	var unit string
	var conversion float64
	if maxBucketLoad < 1024 {
		unit = "B"
		conversion = 1
	} else if maxBucketLoad < 1024*1024 {
		unit = "KB"
		conversion = 1024
	} else if maxBucketLoad < 1024*1024*1024 {
		unit = "MB"
		conversion = 1024 * 1024
	} else {
		unit = "GB"
		conversion = 1024 * 1024 * 1024
	}

	// Convert values, rounding up so any non-zero load displays at least 1 unit.
	histogram := make(map[string]int, numBuckets)
	for i, raw := range histogramSizes {
		var value int
		if raw > 0 {
			value = int((raw + conversion - 1) / conversion)
		} else {
			value = 0
		}
		histogram[bucketLabels[i]] = value
	}

	// Compute scale factor for display (max 40 bar blocks).
	histogramWidth := 40
	maxValue := 0
	for _, v := range histogram {
		if v > maxValue {
			maxValue = v
		}
	}

	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

// computeTempFileCountHistogram calculates a histogram of temporary file count over time.
// It reuses computeTempFileCountHistogramFromEvents with all events.
func computeTempFileCountHistogram(m analysis.TempFileMetrics) (map[string]int, string, int) {
	return computeTempFileCountHistogramFromEvents(m.Events)
}

// computeTempFileCountHistogramFromEvents calculates a histogram of temp file count
// over time from a slice of events (can be filtered or not).
//
// Returns:
//   - histogram: map of time range labels to file count
//   - unit: "" (empty, count has no unit)
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeTempFileCountHistogramFromEvents(events []analysis.TempFileEvent) (map[string]int, string, int) {
	if len(events) == 0 {
		return nil, "", 0
	}

	// Find time range
	start := events[0].Timestamp
	end := events[0].Timestamp
	for _, event := range events {
		if event.Timestamp.Before(start) {
			start = event.Timestamp
		}
		if event.Timestamp.After(end) {
			end = event.Timestamp
		}
	}

	// Divide into 6 buckets
	totalDuration := end.Sub(start)
	if totalDuration < time.Second {
		totalDuration = time.Second
	}
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Prepare buckets
	histogram := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := start.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
	}

	// Count temp files per bucket
	for _, event := range events {
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

	return result, "", scaleFactor
}

// computeCheckpointHistogram calculates a histogram of checkpoint events over a day.
// It divides the day into 6 equal 4-hour buckets.
//
// Returns:
//   - histogram: map of time range labels to checkpoint count
//   - unit: "checkpoints"
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeCheckpointHistogram(m analysis.CheckpointMetrics) (map[string]int, string, int) {
	if len(m.Events) == 0 {
		return nil, "", 0
	}

	// Use the first event's day to anchor the start of the 24h range.
	day := m.Events[0]
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.Add(24 * time.Hour)
	numBuckets := 6
	bucketDuration := end.Sub(start) / time.Duration(numBuckets)

	histogram := make(map[string]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		label := fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogram[label] = 0
		bucketLabels[i] = label
	}

	// Distribute events into buckets based on the hour of day.
	for _, t := range m.Events {
		hour := t.Hour()
		bucketIndex := hour / 4 // 24h / 6 = 4 hours per bucket
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogram[bucketLabels[bucketIndex]]++
	}

	// Compute scale factor for display (max 40 bar blocks).
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

	return histogram, "checkpoints", scaleFactor
}

// WALDistanceBucket holds the average WAL distance and estimate for a time bucket.
type WALDistanceBucket struct {
	Label      string
	AvgDistMB  float64
	AvgEstMB   float64
	Count      int
}

// computeWALDistanceHistogram groups checkpoint WAL distances into 4-hour buckets
// and computes average distance and estimate per bucket.
func computeWALDistanceHistogram(m analysis.CheckpointMetrics) []WALDistanceBucket {
	if len(m.WALDistances) == 0 {
		return nil
	}

	numBuckets := 6
	day := m.WALDistances[0].Timestamp
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())

	buckets := make([]WALDistanceBucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bStart := start.Add(time.Duration(i) * 4 * time.Hour)
		bEnd := bStart.Add(4 * time.Hour)
		buckets[i].Label = fmt.Sprintf("%s - %s", bStart.Format("15:04"), bEnd.Format("15:04"))
	}

	// Accumulate per bucket
	type accum struct {
		totalDist int64
		totalEst  int64
		count     int
	}
	acc := make([]accum, numBuckets)

	for _, w := range m.WALDistances {
		idx := w.Timestamp.Hour() / 4
		if idx >= numBuckets {
			idx = numBuckets - 1
		}
		acc[idx].totalDist += w.DistanceKB
		acc[idx].totalEst += w.EstimateKB
		acc[idx].count++
	}

	for i := 0; i < numBuckets; i++ {
		buckets[i].Count = acc[i].count
		if acc[i].count > 0 {
			buckets[i].AvgDistMB = float64(acc[i].totalDist) / float64(acc[i].count) / 1024.0
			buckets[i].AvgEstMB = float64(acc[i].totalEst) / float64(acc[i].count) / 1024.0
		}
	}

	return buckets
}

// computeConnectionsHistogram calculates a histogram of connection events over time.
// It divides the time range into 6 equal buckets and counts connections in each bucket.
// If logStart/logEnd are provided, they define the time range; otherwise the range is derived from events.
//
// Returns:
//   - histogram: map of time range labels to connection count
//   - unit: "connections"
//   - scaleFactor: for proportional display (max bar width = 40 chars)
func computeConnectionsHistogram(events []time.Time, logStart, logEnd time.Time) (map[string]int, string, int) {
	if len(events) == 0 {
		return nil, "", 0
	}

	// Use log period if provided, otherwise derive from events
	start := logStart
	end := logEnd
	if start.IsZero() || end.IsZero() {
		start = events[0]
		end = events[0]
		for _, t := range events {
			if t.Before(start) {
				start = t
			}
			if t.After(end) {
				end = t
			}
		}
	}

	// Fixed at 6 buckets.
	numBuckets := 6
	totalDuration := end.Sub(start)
	if totalDuration <= 0 {
		totalDuration = time.Second // Minimum duration to avoid division by zero.
	}
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Check if we span multiple calendar days
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	spansDays := !startDay.Equal(endDay)

	// Create buckets with their labels.
	histogram := make(map[string]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		var label string
		if spansDays {
			label = fmt.Sprintf("%02d/%02d %02d:%02d - %02d/%02d %02d:%02d",
				bucketStart.Month(), bucketStart.Day(),
				bucketStart.Hour(), bucketStart.Minute(),
				bucketEnd.Month(), bucketEnd.Day(),
				bucketEnd.Hour(), bucketEnd.Minute())
		} else {
			label = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		}
		histogram[label] = 0
		bucketLabels[i] = label
	}

	// Distribute events into buckets.
	for _, t := range events {
		// Skip events outside the [start, end] range.
		if t.Before(start) || t.After(end) {
			continue
		}
		elapsed := t.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogram[bucketLabels[bucketIndex]]++
	}

	// Compute scale factor for display (max 40 bar blocks).
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

	return histogram, "connections", scaleFactor
}

// computeConcurrentHistogram calculates concurrent sessions over time during the analyzed period.
// It divides the log period (logStart to logEnd) into numBuckets buckets and counts concurrent sessions.
// Returns: histogram data, ordered labels, scale factor, and peak times for each bucket.
func computeConcurrentHistogram(sessions []analysis.SessionEvent, logStart, logEnd time.Time, numBuckets int) (map[string]int, []string, int, map[string]time.Time) {
	if len(sessions) == 0 || logStart.IsZero() || logEnd.IsZero() {
		return nil, nil, 1, nil
	}

	// Use the log period as the reference time range
	minTime := logStart
	maxTime := logEnd

	duration := maxTime.Sub(minTime)
	if duration <= 0 {
		// If duration is negative or zero, swap min and max
		minTime, maxTime = maxTime, minTime
		duration = maxTime.Sub(minTime)
		if duration == 0 {
			return nil, nil, 1, nil
		}
	}

	// Default to 6 buckets if not specified
	if numBuckets <= 0 {
		numBuckets = 6
	}
	bucketDuration := duration / time.Duration(numBuckets)

	// Initialize buckets
	hist := make(map[string]int)
	peakTimes := make(map[string]time.Time)
	labels := make([]string, numBuckets)

	// Check if we span multiple calendar days (not just > 24h, but different dates)
	minDay := time.Date(minTime.Year(), minTime.Month(), minTime.Day(), 0, 0, 0, 0, minTime.Location())
	maxDay := time.Date(maxTime.Year(), maxTime.Month(), maxTime.Day(), 0, 0, 0, 0, maxTime.Location())
	spansDays := !minDay.Equal(maxDay)

	for i := 0; i < numBuckets; i++ {
		bucketStart := minTime.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)

		if spansDays {
			// Include date when spanning multiple days
			labels[i] = fmt.Sprintf("%02d/%02d %02d:%02d - %02d/%02d %02d:%02d",
				bucketStart.Month(), bucketStart.Day(),
				bucketStart.Hour(), bucketStart.Minute(),
				bucketEnd.Month(), bucketEnd.Day(),
				bucketEnd.Hour(), bucketEnd.Minute())
		} else {
			labels[i] = fmt.Sprintf("%02d:%02d - %02d:%02d",
				bucketStart.Hour(), bucketStart.Minute(),
				bucketEnd.Hour(), bucketEnd.Minute())
		}
	}

	// Use sweep line algorithm O(n log n) instead of O(intervals * sessions)
	// Create events: +1 for session start, -1 for session end
	type event struct {
		time  time.Time
		delta int // +1 start, -1 end
	}
	events := make([]event, 0, len(sessions)*2)
	for _, s := range sessions {
		if !s.StartTime.IsZero() && !s.EndTime.IsZero() {
			events = append(events, event{s.StartTime, +1})
			events = append(events, event{s.EndTime, -1})
		}
	}

	// Sort events by time (starts before ends at same time for correct counting)
	sort.Slice(events, func(i, j int) bool {
		if events[i].time.Equal(events[j].time) {
			return events[i].delta > events[j].delta // +1 before -1
		}
		return events[i].time.Before(events[j].time)
	})

	// Track max concurrency and peak time per bucket
	bucketMax := make([]int, numBuckets)
	bucketPeakTime := make([]time.Time, numBuckets)

	current := 0
	eventIdx := 0

	// Process each bucket
	for b := 0; b < numBuckets; b++ {
		bucketStart := minTime.Add(time.Duration(b) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)

		// Initialize bucket max with current count (carried over from previous bucket)
		if current > bucketMax[b] {
			bucketMax[b] = current
			bucketPeakTime[b] = bucketStart
		}

		// Process all events within this bucket
		for eventIdx < len(events) && events[eventIdx].time.Before(bucketEnd) {
			e := events[eventIdx]

			// Skip events before minTime
			if e.time.Before(minTime) {
				current += e.delta
				eventIdx++
				continue
			}

			// Apply delta
			current += e.delta
			if current < 0 {
				current = 0
			}

			// Update max if this event is in our bucket
			if !e.time.Before(bucketStart) && current > bucketMax[b] {
				bucketMax[b] = current
				bucketPeakTime[b] = e.time
			}

			eventIdx++
		}
	}

	// Fill histogram
	for i := 0; i < numBuckets; i++ {
		hist[labels[i]] = bucketMax[i]
		if bucketMax[i] > 0 {
			peakTimes[labels[i]] = bucketPeakTime[i]
		}
	}

	// Calculate scale factor to limit width to 40 chars
	maxValue := 0
	for _, count := range hist {
		if count > maxValue {
			maxValue = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return hist, labels, scaleFactor, peakTimes
}
