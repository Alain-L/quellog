package analysis

import (
	"sort"
	"strings"
	"time"

	"dalibo/quellog/parser"
)

// GlobalMetrics aggregates general log statistics.
type GlobalMetrics struct {
	Count        int
	MinTimestamp time.Time
	MaxTimestamp time.Time
	ErrorCount   int
	FatalCount   int
	PanicCount   int
	WarningCount int
	LogCount     int
}

// UniqueEntityMetrics tracks unique database entities (DBs, Users, Apps).
type UniqueEntityMetrics struct {
	UniqueDbs   int
	UniqueUsers int
	UniqueApps  int
	DBs         []string
	Users       []string
	Apps        []string
}

// AggregatedMetrics is the final structure combining all metrics.
type AggregatedMetrics struct {
	Global         GlobalMetrics
	TempFiles      TempFileMetrics
	Vacuum         VacuumMetrics
	Checkpoints    CheckpointMetrics
	Connections    ConnectionMetrics
	UniqueEntities UniqueEntityMetrics
	EventSummaries []EventSummary
	ErrorClasses   []ErrorClassSummary
	SQL            SqlMetrics
}

// ============================================================================
// VERSION STREAMING (NOUVELLE)
// ============================================================================

// StreamingAnalyzer accumule les métriques au fil de l'eau.
type StreamingAnalyzer struct {
	global         GlobalMetrics
	tempFiles      *TempFileAnalyzer
	vacuum         *VacuumAnalyzer
	checkpoints    *CheckpointAnalyzer
	connections    *ConnectionAnalyzer
	events         *EventAnalyzer
	errorClasses   *ErrorClassAnalyzer
	uniqueEntities *UniqueEntityAnalyzer
	sql            *SQLAnalyzer
}

// NewStreamingAnalyzer crée un nouvel analyseur streaming.
func NewStreamingAnalyzer() *StreamingAnalyzer {
	return &StreamingAnalyzer{
		tempFiles:      NewTempFileAnalyzer(),
		vacuum:         NewVacuumAnalyzer(),
		checkpoints:    NewCheckpointAnalyzer(),
		connections:    NewConnectionAnalyzer(),
		events:         NewEventAnalyzer(),
		errorClasses:   NewErrorClassAnalyzer(),
		uniqueEntities: NewUniqueEntityAnalyzer(),
		sql:            NewSQLAnalyzer(),
	}
}

// Process traite une entrée de log.
func (sa *StreamingAnalyzer) Process(entry *parser.LogEntry) {
	// Global metrics
	sa.global.Count++
	if sa.global.MinTimestamp.IsZero() || entry.Timestamp.Before(sa.global.MinTimestamp) {
		sa.global.MinTimestamp = entry.Timestamp
	}
	if sa.global.MaxTimestamp.IsZero() || entry.Timestamp.After(sa.global.MaxTimestamp) {
		sa.global.MaxTimestamp = entry.Timestamp
	}

	// Dispatch vers les analyseurs spécialisés (chacun filtre lui-même)
	sa.tempFiles.Process(entry)
	sa.vacuum.Process(entry)
	sa.checkpoints.Process(entry)
	sa.connections.Process(entry)
	sa.events.Process(entry)
	sa.errorClasses.Process(entry)
	sa.uniqueEntities.Process(entry)
	sa.sql.Process(entry)
}

// Finalize calcule les métriques finales après traitement de toutes les entrées.
func (sa *StreamingAnalyzer) Finalize() AggregatedMetrics {
	return AggregatedMetrics{
		Global:         sa.global,
		TempFiles:      sa.tempFiles.Finalize(),
		Vacuum:         sa.vacuum.Finalize(),
		Checkpoints:    sa.checkpoints.Finalize(),
		Connections:    sa.connections.Finalize(),
		EventSummaries: sa.events.Finalize(),
		ErrorClasses:   sa.errorClasses.Finalize(),
		UniqueEntities: sa.uniqueEntities.Finalize(),
		SQL:            sa.sql.Finalize(),
	}
}

// AggregateMetrics est maintenant une simple façade pour le streaming analyzer.
// ✅ VERSION STREAMING - Plus d'accumulation en mémoire !
func AggregateMetrics(in <-chan parser.LogEntry) AggregatedMetrics {
	analyzer := NewStreamingAnalyzer()

	// ✅ Traitement au fil de l'eau, SANS accumulation
	for entry := range in {
		analyzer.Process(&entry)
	}

	return analyzer.Finalize()
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards - À SUPPRIMER)
// ============================================================================

// AggregateMetricsOld - Ancienne version qui accumule tout en mémoire.
// Cette fonction est conservée temporairement pour référence.
// À SUPPRIMER une fois tous les tests validés.
func AggregateMetricsOld(in <-chan parser.LogEntry) AggregatedMetrics {
	var metrics AggregatedMetrics
	var allEntries []parser.LogEntry

	// ❌ ANCIEN CODE - Accumule TOUT en mémoire
	for entry := range in {
		allEntries = append(allEntries, entry)
		metrics.Global.Count++
		if metrics.Global.MinTimestamp.IsZero() || entry.Timestamp.Before(metrics.Global.MinTimestamp) {
			metrics.Global.MinTimestamp = entry.Timestamp
		}
		if metrics.Global.MaxTimestamp.IsZero() || entry.Timestamp.After(metrics.Global.MaxTimestamp) {
			metrics.Global.MaxTimestamp = entry.Timestamp
		}
	}

	metrics.TempFiles = CalculateTemporaryFileMetrics(&allEntries)
	AnalyzeVacuum(&metrics.Vacuum, &allEntries)
	metrics.Checkpoints = AnalyzeCheckpoints(&allEntries)
	metrics.EventSummaries = SummarizeEvents(&allEntries)
	metrics.Connections = AnalyzeConnections(&allEntries)
	metrics.UniqueEntities = AnalyzeUniqueEntities(&allEntries)

	sqlLogs := make(chan parser.LogEntry, len(allEntries))
	for _, entry := range allEntries {
		sqlLogs <- entry
	}
	close(sqlLogs)
	metrics.SQL = RunSQLSummary(sqlLogs)

	return metrics
}

// ============================================================================
// ANALYSEUR UNIQUE ENTITIES (intégré dans le streaming)
// ============================================================================

// UniqueEntityAnalyzer traite les entités uniques au fil de l'eau.
type UniqueEntityAnalyzer struct {
	dbSet   map[string]struct{}
	userSet map[string]struct{}
	appSet  map[string]struct{}
}

func NewUniqueEntityAnalyzer() *UniqueEntityAnalyzer {
	return &UniqueEntityAnalyzer{
		dbSet:   make(map[string]struct{}, 100),
		userSet: make(map[string]struct{}, 100),
		appSet:  make(map[string]struct{}, 100),
	}
}

func (a *UniqueEntityAnalyzer) Process(entry *parser.LogEntry) {
	msg := &entry.Message

	// Extract database name
	if dbName, found := extractKeyValue(*msg, "db="); found {
		a.dbSet[dbName] = struct{}{}
	}
	// Extract user name
	if userName, found := extractKeyValue(*msg, "user="); found {
		a.userSet[userName] = struct{}{}
	}
	// Extract application name
	if appName, found := extractKeyValue(*msg, "app="); found {
		a.appSet[appName] = struct{}{}
	}
}

func (a *UniqueEntityAnalyzer) Finalize() UniqueEntityMetrics {
	return UniqueEntityMetrics{
		UniqueDbs:   len(a.dbSet),
		UniqueUsers: len(a.userSet),
		UniqueApps:  len(a.appSet),
		DBs:         mapKeysAsSlice(a.dbSet),
		Users:       mapKeysAsSlice(a.userSet),
		Apps:        mapKeysAsSlice(a.appSet),
	}
}

// ============================================================================
// ANCIENNE VERSION AnalyzeUniqueEntities (compatibilité backwards)
// ============================================================================

// AnalyzeUniqueEntities - Ancienne version à supprimer après migration.
func AnalyzeUniqueEntities(entries *[]parser.LogEntry) UniqueEntityMetrics {
	analyzer := NewUniqueEntityAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}

// ============================================================================
// HELPERS (inchangés)
// ============================================================================

// extractKeyValue extracts a value from a log message based on a given key (e.g., "db=").
func extractKeyValue(line, key string) (string, bool) {
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len(key):]

	separators := []rune{' ', ',', '[', ')'}
	minPos := len(rest)
	for _, sep := range separators {
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < minPos {
			minPos = pos
		}
	}

	val := strings.TrimSpace(rest[:minPos])
	if val == "" || strings.EqualFold(val, "unknown") || strings.EqualFold(val, "[unknown]") {
		val = "UNKNOWN"
	}
	return val, true
}

// mapKeysAsSlice converts a map's keys into a sorted slice.
func mapKeysAsSlice(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
