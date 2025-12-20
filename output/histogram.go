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
func computeQueryLoadHistogram(m analysis.SqlMetrics) (map[string]int, string, int) {
	if m.StartTimestamp.IsZero() || m.EndTimestamp.IsZero() || len(m.Executions) == 0 {
		return nil, "", 0
	}

	// Division de l'intervalle total en 6 buckets égaux.
	totalDuration := m.EndTimestamp.Sub(m.StartTimestamp)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Protection contre la division par zéro : si la durée totale est nulle ou très courte
	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Préparation des buckets avec accumulation en millisecondes.
	histogramMs := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		start := m.StartTimestamp.Add(time.Duration(i) * bucketDuration)
		end := m.StartTimestamp.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", start.Format("15:04"), end.Format("15:04"))
	}

	// Répartition des durées (en ms) dans les buckets en fonction du timestamp de chaque exécution.
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

	// Détermination de l'unité et du facteur de conversion en fonction de la charge maximale.
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

	// Conversion et arrondi vers le haut pour que toute charge non nulle soit affichée au moins 1 unité.
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

	// Calcul automatique du scale factor pour l'affichage.
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
func computeQueryDurationHistogram(m analysis.SqlMetrics) (map[string]int, string, int) {
	// Définition des buckets fixes dans l'ordre souhaité.
	bucketDefinitions := []struct {
		label string
		lower float64 // Borne inférieure en ms (inclusive)
		upper float64 // Borne supérieure en ms (exclusive)
	}{
		{"< 1 ms", 0, 1},
		{"< 10 ms", 1, 10},
		{"< 100 ms", 10, 100},
		{"< 1 s", 100, 1000},
		{"< 10 s", 1000, 10000},
		{">= 10 s", 10000, math.Inf(1)},
	}

	// Initialisation de l'histogramme avec des valeurs à zéro pour garantir l'ordre d'affichage.
	histogram := make(map[string]int)
	for _, bucket := range bucketDefinitions {
		histogram[bucket.label] = 0
	}

	// Parcours des requêtes et répartition dans les buckets.
	for _, exec := range m.Executions {
		d := exec.Duration
		for _, bucket := range bucketDefinitions {
			if d >= bucket.lower && d < bucket.upper {
				histogram[bucket.label]++
				break
			}
		}
	}

	// Détermination du nombre maximal de requêtes dans un bucket.
	maxCount := 0
	for _, count := range histogram {
		if count > maxCount {
			maxCount = count
		}
	}

	// L'unité est "req" (nombre de requêtes).
	unit := "req"

	// Calcul du scale factor pour limiter la barre la plus longue à 40 caractères.
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
	// Si aucun événement, on ne peut pas construire l'histogramme.
	if len(m.Events) == 0 {
		return nil, "", 0
	}

	// Déterminer le début et la fin de la période à partir des événements.
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

	// Si tous les événements ont le même timestamp (ou très proche),
	// on ne peut pas créer un histogramme temporel utile.
	// On met tout dans un seul bucket.
	if totalDuration < time.Second {
		totalDuration = time.Second
	}

	// On divise l'intervalle en 6 buckets égaux.
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Préparation des buckets.
	histogramSizes := make([]float64, numBuckets) // cumul des tailles en octets pour chaque bucket
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogramSizes[i] = 0
	}

	// Répartition des événements dans les buckets.
	for _, event := range m.Events {
		elapsed := event.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogramSizes[bucketIndex] += event.Size
	}

	// Détermination de la charge maximale.
	maxBucketLoad := 0.0
	for _, size := range histogramSizes {
		if size > maxBucketLoad {
			maxBucketLoad = size
		}
	}

	// Choix de l'unité et du facteur de conversion en fonction de la charge maximale.
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

	// Conversion des valeurs en arrondissant vers le haut pour afficher au moins 1 unité.
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

	// Calcul automatique du scale factor pour l'affichage (limite à 40 blocs max).
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

	// On utilise le jour de la première occurrence pour fixer le début de la journée.
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

	// Répartition des événements dans les buckets, en se basant sur l'heure de l'événement.
	for _, t := range m.Events {
		// Si l'événement n'est pas dans la journée, on le ramène dans l'intervalle [start, end).
		// On utilise l'heure uniquement.
		hour := t.Hour()
		bucketIndex := hour / 4 // 24h/6 = 4 heures par bucket.
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogram[bucketLabels[bucketIndex]]++
	}

	// Calcul du scale factor pour limiter la barre à 35 caractères.
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

	// On fixe le nombre de buckets à 6.
	numBuckets := 6
	totalDuration := end.Sub(start)
	if totalDuration <= 0 {
		totalDuration = time.Second // Durée minimale pour éviter la division par zéro.
	}
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Check if we span multiple calendar days
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	spansDays := !startDay.Equal(endDay)

	// Création des buckets avec leurs labels.
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

	// Répartition des événements dans les buckets.
	for _, t := range events {
		// On s'assure que l'événement se situe dans l'intervalle [start, end].
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

	// Calcul du scale factor pour limiter la largeur de la barre à 35 caractères.
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

	// L'unité ici est "connections".
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
