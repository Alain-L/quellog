// analysis/summary.go
package analysis

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dalibo/quellog/parser"

	"golang.org/x/term"
)

// Metrics aggregates reporting data.
// (future: move this to analysis/aggregator.go)
type Metrics struct {
	Count        int
	MinTimestamp time.Time
	MaxTimestamp time.Time
	ErrorCount   int
	FatalCount   int
	PanicCount   int
	WarningCount int
	LogCount     int

	// Temp files
	TempFileCount     int
	TempFileTotalSize int64

	// Maintenance metrics
	VacuumCount          int              // Total automatic vacuum operations
	AnalyzeCount         int              // Total automatic analyze operations
	VacuumTableCounts    map[string]int   // vacuum operations per table
	AnalyzeTableCounts   map[string]int   // analyze operations per table
	VacuumSpaceRecovered map[string]int64 // Espace récupéré par table en octets

	// Checkpoint metrics (maintenant, uniquement sur "checkpoint complete" avec write time)
	CheckpointCompleteCount     int     // Nombre de checkpoints complets
	TotalCheckpointWriteSeconds float64 // Somme des temps d'écriture (extrait de "write=xxx s")

	// Connection & session metrics
	ConnectionReceivedCount int           // Nombre de connexions reçues
	DisconnectionCount      int           // Nombre d'événements de déconnexion
	TotalSessionTime        time.Duration // Durée totale cumulée des sessions

	UniqueDbs   int
	UniqueUsers int
	UniqueApps  int

	DBs   []string
	Users []string
	Apps  []string
}

// AggregateMetrics consumes log entries from the channel, aggregates metrics in real time,
// and returns the final Metrics structure when the channel is closed.
func AggregateMetrics(in <-chan parser.LogEntry) Metrics {
	var m Metrics

	// Utiliser des maps pour collecter les valeurs uniques
	dbSet := make(map[string]struct{})
	userSet := make(map[string]struct{})
	appSet := make(map[string]struct{})

	// Utiliser des maps pour collecter les valeurs uniques
	m.VacuumTableCounts = make(map[string]int)
	m.AnalyzeTableCounts = make(map[string]int)
	m.VacuumSpaceRecovered = make(map[string]int64)

	// Précompiler la regex pour extraire la taille d'un fichier temporaire.
	// On attend un pattern du type "size <number>"
	tempSizeRegex := regexp.MustCompile(`size\s+(\d+)`)

	// Précompiler la regex pour extraire le nom de la table pour vacuum/analyze.
	// On attend un pattern du type : automatic vacuum of table "schema.table"
	// ou automatic analyze of table "schema.table"
	analyzeRegex := regexp.MustCompile(`automatic analyze of table "([^"]+)"`)

	// Précompiler la regex pour extraire le nombre de pages supprimées.
	// Ex : pages: 74 removed, ...
	vacuumRegex := regexp.MustCompile(`(?i)automatic\s+vacuum\s+of\s+table\s+"([^"]+)"(?:.*pages:\s+(\d+)\s+removed)?`)

	// Pour extraire le temps de session, par exemple "session time: 0:00:00.004"
	sessionRegex := regexp.MustCompile(`(?i)session time:\s*([\d\:\.]+)`)
	// Pour les connexions, on cherche "connection received:" (insensible à la casse)
	// Pour les déconnexions, on cherche "disconnection:" (et on extraira la session time)

	for entry := range in {
		// On ignore les entrées sans timestamp valide
		if entry.Timestamp.IsZero() {
			continue
		}

		m.Count++

		// Update min and max timestamps
		if m.MinTimestamp.IsZero() || entry.Timestamp.Before(m.MinTimestamp) {
			m.MinTimestamp = entry.Timestamp
		}
		if m.MaxTimestamp.IsZero() || entry.Timestamp.After(m.MaxTimestamp) {
			m.MaxTimestamp = entry.Timestamp
		}

		// Count keyword occurrences
		if strings.Contains(entry.Message, "ERROR") {
			m.ErrorCount++
		}
		if strings.Contains(entry.Message, "FATAL") {
			m.FatalCount++
		}
		if strings.Contains(entry.Message, "PANIC") {
			m.PanicCount++
		}
		if strings.Contains(entry.Message, "WARNING") {
			m.WarningCount++
		}
		if strings.Contains(entry.Message, "LOG:") {
			m.LogCount++
		}

		// Analyse des messages relatifs aux fichiers temporaires
		if strings.Contains(entry.Message, "temporary file:") {
			m.TempFileCount++
			// Rechercher la taille avec la regex
			matches := tempSizeRegex.FindStringSubmatch(entry.Message)
			if len(matches) >= 2 {
				if size, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
					m.TempFileTotalSize += size
				}
			}
		}

		// Maintenance metrics
		if strings.Contains(strings.ToLower(entry.Message), "automatic vacuum") {
			m.VacuumCount++
		}
		if strings.Contains(strings.ToLower(entry.Message), "automatic analyze") {
			m.AnalyzeCount++
		}

		// Maintenance : opérations vacuum/analyze sur la même ligne.
		lowerMsg := strings.ToLower(entry.Message)
		if strings.Contains(lowerMsg, "automatic vacuum of table") {
			matches := vacuumRegex.FindStringSubmatch(entry.Message)
			if len(matches) >= 2 {
				tableName := matches[1]
				m.VacuumCount++
				m.VacuumTableCounts[tableName]++
				// Si le nombre de pages supprimées est présent (groupe 2 non vide)
				if len(matches) >= 3 && matches[2] != "" {
					if pages, err := strconv.Atoi(matches[2]); err == nil {
						// On suppose que chaque page fait 8KB
						m.VacuumSpaceRecovered[tableName] += int64(pages) * 8192
					}
				}
				// On passe à la prochaine entrée
				continue
			}
		}

		// analyze
		if strings.Contains(lowerMsg, "automatic analyze of table") {
			matches := analyzeRegex.FindStringSubmatch(entry.Message)
			// On peut réutiliser la même regex que vacuum éventuellement
			if len(matches) >= 2 {
				tableName := matches[1]
				m.AnalyzeCount++
				m.AnalyzeTableCounts[tableName]++
			}
		}

		// Checkpoint metrics: calculer la durée d'écriture à partir de "checkpoint complete:".
		// On utilise une regex pour extraire la valeur en secondes après "write=".
		writeRegex := regexp.MustCompile(`(?i)write=\s*([\d\.]+)\s*s`)
		if strings.Contains(strings.ToLower(entry.Message), "checkpoint complete:") {
			m.CheckpointCompleteCount++
			matches := writeRegex.FindStringSubmatch(entry.Message)
			if len(matches) >= 2 {
				if secs, err := strconv.ParseFloat(matches[1], 64); err == nil {
					m.TotalCheckpointWriteSeconds += secs
				}
			}
		}

		// Connection & session metrics
		if strings.Contains(lowerMsg, "connection received:") {
			m.ConnectionReceivedCount++
		}
		if strings.Contains(lowerMsg, "disconnection:") {
			m.DisconnectionCount++
			// Extraire le temps de session de déconnexion, par exemple "session time: 0:00:00.004"
			sessionMatches := sessionRegex.FindStringSubmatch(entry.Message)
			if len(sessionMatches) >= 2 {
				if sessionDuration, err := parseSessionTime(sessionMatches[1]); err == nil {
					m.TotalSessionTime += sessionDuration
				}
			}
		}

		// Extract and accumulate unique values for DB, user, and app.
		if dbName, found := extractKeyValue(entry.Message, "db="); found {
			if strings.TrimSpace(dbName) == "" {
				dbName = "UNKNOWN"
			}
			dbSet[dbName] = struct{}{}
		}
		if userName, found := extractKeyValue(entry.Message, "user="); found {
			if strings.TrimSpace(userName) == "" {
				userName = "UNKNOWN"
			}
			userSet[userName] = struct{}{}
		}
		if appName, found := extractKeyValue(entry.Message, "app="); found {
			if strings.TrimSpace(appName) == "" {
				appName = "UNKNOWN"
			}
			appSet[appName] = struct{}{}
		}
	}

	// Convertir les sets en slices triées
	m.DBs = mapKeysAsSlice(dbSet)
	m.Users = mapKeysAsSlice(userSet)
	m.Apps = mapKeysAsSlice(appSet)

	m.UniqueDbs = len(m.DBs)
	m.UniqueUsers = len(m.Users)
	m.UniqueApps = len(m.Apps)

	return m
}

// PrintMetrics displays the aggregated metrics.
// (future: move this to output/formatter.go)
func PrintMetrics(m Metrics) {
	// Calculer la durée totale à partir des timestamps min et max.
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)

	// Préparer le style ANSI pour le texte en gras.
	bold := "\033[1m"
	reset := "\033[0m"

	// Affichage de l'en-tête général.
	fmt.Println(bold + "\nSUMMARY" + reset)
	fmt.Printf("\n  %-25s : %s\n", "Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "Duration", duration)
	fmt.Printf("  %-25s : %d\n", "Total entries", m.Count)
	if duration > 0 {
		rate := float64(m.Count) / duration.Seconds()
		fmt.Printf("  %-25s : %.2f entries/s\n", "Throughput", rate)
	}
	fmt.Printf("  %-25s : %d\n", "Entries with 'ERROR'", m.ErrorCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'FATAL'", m.FatalCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'PANIC'", m.PanicCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'WARNING'", m.WarningCount)
	fmt.Printf("  %-25s : %d\n", "Log entries ('LOG:')", m.LogCount)

	// Section Temp Files
	fmt.Println(bold + "\nTemp files" + reset)
	fmt.Printf("  %-25s : %d\n", "Temp file messages", m.TempFileCount)
	fmt.Printf("  %-25s : %s\n", "Cumulative temp file size", formatBytes(m.TempFileTotalSize))
	avgSize := int64(0)
	if m.TempFileCount > 0 {
		avgSize = m.TempFileTotalSize / int64(m.TempFileCount)
	}
	fmt.Printf("  %-25s : %s\n", "Average temp file size", formatBytes(avgSize))

	// Section Maintenance Metrics
	fmt.Println(bold + "\nMaintenance" + reset)
	fmt.Printf("  %-25s : %d\n", "Automatic vacuum count", m.VacuumCount)
	fmt.Printf("  %-25s : %d\n", "Automatic analyze count", m.AnalyzeCount)
	fmt.Println("  Top automatic vacuum operations per table:")
	printTopTables(m.VacuumTableCounts, m.VacuumCount, m.VacuumSpaceRecovered)
	fmt.Println("  Top automatic analyze operations per table:")
	printTopTables(m.AnalyzeTableCounts, m.AnalyzeCount, nil)

	// Section Checkpoints (si disponible)
	if m.CheckpointCompleteCount > 0 {
		avgWriteSeconds := m.TotalCheckpointWriteSeconds / float64(m.CheckpointCompleteCount)
		avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
		fmt.Println(bold + "\nCHECKPOINTS" + reset)
		fmt.Printf("  %-25s : %d\n", "Checkpoint count", m.CheckpointCompleteCount)
		fmt.Printf("  %-25s : %s\n", "Avg checkpoint write time", avgDuration)
	}

	// Section Connection & Session Metrics
	fmt.Println(bold + "\nConnections & Sessions" + reset)
	fmt.Printf("  %-25s : %d\n", "Connection count", m.ConnectionReceivedCount)
	if duration.Hours() > 0 {
		avgConnPerHour := float64(m.ConnectionReceivedCount) / duration.Hours()
		fmt.Printf("  %-25s : %d\n", "Avg connections per hour", int(avgConnPerHour))
	}
	fmt.Printf("  %-25s : %d\n", "Disconnection count", m.DisconnectionCount)
	if m.DisconnectionCount > 0 {
		avgSessionTime := m.TotalSessionTime / time.Duration(m.DisconnectionCount)
		fmt.Printf("  %-25s : %s\n", "Avg session time", avgSessionTime.Truncate(time.Second))
	}

	// Section Unique Clients
	fmt.Println(bold + "\nCLIENTS" + reset)
	fmt.Printf("  %-25s : %d\n", "Unique DBs", m.UniqueDbs)
	fmt.Printf("  %-25s : %d\n", "Unique Users", m.UniqueUsers)
	fmt.Printf("  %-25s : %d\n", "Unique Apps", m.UniqueApps)

	// Affichage des listes
	fmt.Println(bold + "\nDATABASES" + reset)
	for _, db := range m.DBs {
		fmt.Printf("    %s\n", db)
	}
	fmt.Println(bold + "\nUSERS" + reset)
	for _, user := range m.Users {
		fmt.Printf("    %s\n", user)
	}
	fmt.Println(bold + "\nAPPS" + reset)
	for _, app := range m.Apps {
		fmt.Printf("    %s\n", app)
	}

	// Section SQL (si vous souhaitez afficher d'autres informations SQL)
	// ...
}

// Helpers

// mapKeysAsSlice converts a map[string]struct{} into a sorted slice of strings.
func mapKeysAsSlice(m map[string]struct{}) []string {
	var result []string
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// extractKeyValue is a minimal example to get db=, user=, etc. (à bouger dans parser)
func extractKeyValue(line, key string) (string, bool) {
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len(key):]

	// Adapt the set of separators to your log format
	seps := []rune{' ', ',', '[', ')'}
	minPos := len(rest)
	for _, c := range seps {
		if pos := strings.IndexRune(rest, c); pos != -1 && pos < minPos {
			minPos = pos
		}
	}
	val := strings.TrimSpace(rest[:minPos])
	if val == "" || strings.EqualFold(val, "unknown") || strings.EqualFold(val, "[unknown]") {
		val = "UNKNOWN"
	}
	return val, true
}

// formatBytes converts a size in bytes to a human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(GB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// printTopTables prints the top tables for a given operation type (vacuum or analyze)
// en s'arrêtant lorsque le cumul atteint au moins 80% du total, sauf si moins de 10 tables.
func printTopTables(tableCounts map[string]int, total int, spaceRecovered map[string]int64) {
	// Transformer la map en slice de paires
	type tablePair struct {
		Name      string
		Count     int
		Recovered int64 // en octets
	}
	var pairs []tablePair
	for name, count := range tableCounts {
		p := tablePair{
			Name:  name,
			Count: count,
		}
		if spaceRecovered != nil {
			p.Recovered = spaceRecovered[name]
		}
		pairs = append(pairs, p)
	}

	// Trier par Count décroissant
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Count > pairs[j].Count
	})

	// format space for table name
	tableLen := 0
	for _, p := range pairs {
		if l := len(p.Name); l > tableLen {
			tableLen = l
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		if tableLen > int(float64(w)*0.4) {
			tableLen = int(float64(w) * 0.4)
		}
	}

	cum := 0
	n := 0
	for _, pair := range pairs {
		percentage := float64(pair.Count) / float64(total) * 100
		cum += pair.Count
		n++
		cumPercentage := float64(cum) / float64(total) * 100

		// Affichage avec alignement fixe :
		// - Nom de la table: largeur 50, aligné à gauche.
		// - Count: largeur 6, aligné à droite.
		// - Pourcentage: largeur 6, aligné à droite, avec 2 décimales.
		if spaceRecovered != nil && pair.Recovered > 0 {
			fmt.Printf("    %-*s %6d %6.2f%%  %12s removed\n",
				tableLen, pair.Name, pair.Count, percentage, formatBytes(pair.Recovered))
		} else {
			fmt.Printf("    %-*s %6d %6.2f%%\n",
				tableLen, pair.Name, pair.Count, percentage)
		}

		// On s'arrête si le cumul atteint 80% du total et si on a déjà affiché 10 tables.
		if cumPercentage >= 80 || n >= 10 {
			break
		}
	}
}

// parseSessionTime convertit une chaîne au format "HH:MM:SS.mmm" en time.Duration.
func parseSessionTime(s string) (time.Duration, error) {
	// Exemple: "0:00:00.004" → heures, minutes, secondes, millisecondes
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("format invalide")
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	// La partie secondes peut contenir des millisecondes, par ex "00.004"
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	totalSeconds := float64(hours*3600+minutes*60) + seconds
	return time.Duration(totalSeconds * float64(time.Second)), nil
}
