// analysis/summary.go
package analysis

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dalibo/quellog/parser"
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
				if sessionDuration, err := parser.ParseSessionTime(sessionMatches[1]); err == nil {
					m.TotalSessionTime += sessionDuration
				}
			}
		}

		// Extract and accumulate unique values for DB, user, and app.
		if dbName, found := parser.ExtractKeyValue(entry.Message, "db="); found {
			if strings.TrimSpace(dbName) == "" {
				dbName = "UNKNOWN"
			}
			dbSet[dbName] = struct{}{}
		}
		if userName, found := parser.ExtractKeyValue(entry.Message, "user="); found {
			if strings.TrimSpace(userName) == "" {
				userName = "UNKNOWN"
			}
			userSet[userName] = struct{}{}
		}
		if appName, found := parser.ExtractKeyValue(entry.Message, "app="); found {
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
