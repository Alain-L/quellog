// analysis/sql.go
package analysis

import (
	"dalibo/quellog/parser"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// QueryStat stocke les statistiques agrégées pour UNE requête SQL.
type QueryStat struct {
	RawQuery        string // Requête brute d'origine
	NormalizedQuery string // Requête normalisée (utilisée pour les stats)
	Count           int
	TotalTime       float64 // en millisecondes
	AvgTime         float64
	MaxTime         float64
	ID              string // Identifiant "joli" généré, par exemple "se-123xaB"
	FullHash        string // Hash complet en hexadécimal (par exemple, 32 caractères MD5)
}

type sqlMetrics struct {
	QueryStats       map[string]*QueryStat // agrégation par requête normalisée
	TotalQueries     int                   // total des requêtes SQL parsées
	UniqueQueries    int                   // nombre de requêtes uniques (normalisées)
	MinQueryDuration float64               // durée minimale en ms
	MaxQueryDuration float64               // durée maximale en ms
	SumQueryDuration float64               // somme des durées en ms
	StartTimestamp   time.Time             // timestamp de la première requête SQL
	EndTimestamp     time.Time             // timestamp de la dernière requête SQL
	// champs pour la distribution
	Durations           []float64 // Durées de chaque requête (en ms) // TODO histogramme
	MedianQueryDuration float64   // Médiane (50ème percentile)
	P99QueryDuration    float64   // 99ème percentile
}

// RunSQLSummary lit les entrées du canal (filteredLogs) et agrège les statistiques SQL.
// On s'appuie sur des regex pour extraire la durée et le texte de la requête.
func RunSQLSummary(in <-chan parser.LogEntry) sqlMetrics {
	m := sqlMetrics{
		QueryStats: make(map[string]*QueryStat),
	}

	// Regex pour capturer la durée (en ms) d'une requête SQL.
	// Ex : "duration: 123.45 ms"
	// durationRegex := regexp.MustCompile(`duration:\s+([\d\.]+)\s+ms`)
	durationRegex := regexp.MustCompile(`duration:\s+(\d+\.?\d*)\s+ms\b`) // mieux

	// Regex pour capturer la requête SQL.
	// Ex : "STATEMENT:  SELECT * FROM table WHERE id = 1"
	// statementRegex := regexp.MustCompile(`(?i)statement:\s+(.+)`) // no execute
	statementRegex := regexp.MustCompile(`(?i)(?:statement|execute(?:\s+\S+)?):\s+(.+)`)

	// loop over SQL entries
	for entry := range in {
		msg := entry.Message
		// Rechercher la durée et la requête dans le message.
		durationMatch := durationRegex.FindStringSubmatch(msg)
		statementMatch := statementRegex.FindStringSubmatch(msg)
		if len(durationMatch) < 2 || len(statementMatch) < 2 {
			// Ce n'est pas un log SQL avec durée et statement, on passe.
			continue
		}

		// Extraire la durée
		duration, err := strconv.ParseFloat(durationMatch[1], 64)
		if err != nil {
			continue
		}

		// Mettre à jour les indicateurs globaux.
		m.TotalQueries++
		m.Durations = append(m.Durations, duration) // store individual durations in the map
		if m.StartTimestamp.IsZero() || entry.Timestamp.Before(m.StartTimestamp) {
			m.StartTimestamp = entry.Timestamp
		}
		if m.EndTimestamp.IsZero() || entry.Timestamp.After(m.EndTimestamp) {
			m.EndTimestamp = entry.Timestamp
		}

		// Extraire la requête brute
		rawQuery := strings.TrimSpace(statementMatch[1])
		// Normaliser la requête pour grouper les requêtes similaires.
		normalized := normalizeQuery(rawQuery)

		// Générer un identifiant "joli" et le hash complet à partir de la requête
		id, fullHash := generateQueryID(rawQuery, normalized)

		// Utiliser la requête normalisée comme clé dans la map
		key := normalized

		// Update per query stats
		if _, exists := m.QueryStats[key]; !exists {
			m.QueryStats[key] = &QueryStat{
				RawQuery:        rawQuery,
				NormalizedQuery: normalized,
				ID:              id,
				FullHash:        fullHash,
			}
		}
		stats := m.QueryStats[key]
		stats.Count++
		stats.TotalTime += duration
		if duration > stats.MaxTime {
			stats.MaxTime = duration
		}
		stats.AvgTime = stats.TotalTime / float64(stats.Count)

		// Update global stats
		if m.MinQueryDuration == 0 || duration < m.MinQueryDuration {
			m.MinQueryDuration = duration
		}
		if duration > m.MaxQueryDuration {
			m.MaxQueryDuration = duration
		}
		m.SumQueryDuration += duration // TODO check reliability
	}

	// durations histogram
	if len(m.Durations) > 0 {
		sort.Float64s(m.Durations) // TODO avoid sort if feasable
		n := len(m.Durations)
		if n%2 == 1 {
			m.MedianQueryDuration = m.Durations[n/2]
		} else {
			m.MedianQueryDuration = (m.Durations[n/2-1] + m.Durations[n/2]) / 2.0
		}
		index := int(0.99 * float64(n))
		if index >= n {
			index = n - 1
		}
		m.P99QueryDuration = m.Durations[index]
	}

	// Nombre de requêtes uniques (normalisées)
	m.UniqueQueries = len(m.QueryStats)
	return m
}

// SearchQueries parcourt la slice queryDetails et affiche le détail de chaque requête correspondante.
// TODO : Use a struct and separate printing
// SearchQueries parcourt la map QueryStats et affiche les détails pour chaque requête
// dont le SQLID correspond à l'un des éléments de queryDetails.
func SearchQueries(m sqlMetrics, queryDetails []string) {
	// Pour chaque QueryStat dans la map
	for _, qs := range m.QueryStats {
		// Vérifier pour chaque SQLID demandé
		for _, qid := range queryDetails {
			// Comparaison simple (vous pouvez utiliser strings.EqualFold si besoin d'une comparaison insensible à la casse)
			if qs.ID == qid {
				fmt.Printf("\nDetails for SQLID: %s\n", qs.ID)
				fmt.Println("SQL Query Details:")
				fmt.Printf("  SQLID            : %s\n", qs.ID)
				fmt.Printf("  Query Type       : %s\n", queryTypeFromID(qs.ID))
				fmt.Printf("  Raw Query        : %s\n", qs.RawQuery)
				fmt.Printf("  Normalized Query : %s\n", qs.NormalizedQuery)
				fmt.Printf("  Executed         : %d\n", qs.Count)
				fmt.Printf("  Total Time       : %s\n", formatQueryDuration(qs.TotalTime))
				fmt.Printf("  Median Time      : %s\n", formatQueryDuration(qs.AvgTime))
				fmt.Printf("  Max Time         : %s\n", formatQueryDuration(qs.MaxTime))
				// Une fois trouvée, on peut passer à la requête suivante dans la map
			}
		}
	}
}

// PrintSQLSummary affiche un rapport de performance SQL en CLI.
// Le rapport est formaté en gras et en couleur ANSI pour améliorer la lisibilité.
// La requête est tronquée selon la largeur du terminal ; si insuffisant, on affiche son hash (optionnel).
func PrintSQLSummary(m sqlMetrics) {
	// Récupérer la largeur du terminal, avec une valeur par défaut
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width = w
	}
	// On détermine la largeur maximale allouée pour la requête (par exemple, 60% de la largeur)
	queryWidth := int(float64(width) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	// Préparer le style ANSI
	bold := "\033[1m"
	reset := "\033[0m"
	//blue := "\033[34m"

	// Calcul de la durée totale (période couverte par les logs SQL)
	totalDuration := m.EndTimestamp.Sub(m.StartTimestamp)
	avgLoad := float64(m.TotalQueries) / m.EndTimestamp.Sub(m.StartTimestamp).Seconds()

	// Affichage de l'en-tête général.
	fmt.Println("Total log duration: ", formatQueryDuration(float64(totalDuration.Milliseconds())))
	fmt.Println(bold + "\nSQL PERFORMANCE REPORT" + reset)
	fmt.Println()
	fmt.Printf("  %-25s : %-20s  %-25s : %s\n",
		"Total query duration", formatQueryDuration(m.SumQueryDuration),
		"Query max duration", formatQueryDuration(m.MaxQueryDuration))
	fmt.Printf("  %-25s : %-20d  %-25s : %s\n",
		"Total query parsed", m.TotalQueries,
		"Query min duration", formatQueryDuration(m.MinQueryDuration))
	fmt.Printf("  %-25s : %-20d  %-25s : %s\n",
		"Total individual query", m.UniqueQueries,
		"Query median duration", formatQueryDuration(m.MedianQueryDuration))
	fmt.Printf("  %-25s : %-20s  %-25s : %s\n",
		"Average load", formatAverageLoad(avgLoad),
		"Query 99th percentile", formatQueryDuration(m.P99QueryDuration))
	fmt.Println()

	// Affichage du Top 10 Slowest Queries (trié par maxTime)
	fmt.Println(bold + "Slowest individual queries:" + reset)
	printSlowestQueries(m.QueryStats)
	fmt.Println()

	// Affichage du Top 10 Most Frequent Queries (trié par Count)
	fmt.Println(bold + "Most Frequent Individual Queries:" + reset)
	printMostFrequentQueries(m.QueryStats)
	fmt.Println()

	// Affichage du Top 10 Heaviest Queries (trié par TotalTime)
	fmt.Println(bold + "Most time consuming queries :" + reset)
	printTimeConsumingQueries(m.QueryStats)
	fmt.Println()
}

// printTimeConsumingQueries trie et affiche le top 10 des requêtes en fonction du critère donné ("max", "count" ou "total").
// La fonction s'adapte à la largeur du terminal et bascule entre deux modes d'affichage.
func printTimeConsumingQueries(queryStats map[string]*QueryStat) {
	// Récupérer la largeur du terminal (avec une valeur par défaut)
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}

	// Préparer une structure locale pour rassembler les informations.
	type queryInfo struct {
		Query     string // requête normalisée
		ID        string // identifiant "joli"
		Count     int
		TotalTime float64 // en millisecondes
		AvgTime   float64 // en millisecondes
		MaxTime   float64 // en millisecondes
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := generateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			Query:     normalized,
			ID:        id,
			Count:     stats.Count,
			TotalTime: stats.TotalTime,
			AvgTime:   stats.AvgTime,
			MaxTime:   stats.MaxTime,
		})
	}

	// Tri sur le temps cumulé
	sort.Slice(queries, func(i, j int) bool { return queries[i].TotalTime > queries[j].TotalTime })

	// Style ANSI pour l'en-tête (gras)
	bold := "\033[1m"
	reset := "\033[0m"

	// Seuil pour basculer entre mode complet et mode simplifié.
	if termWidth >= 120 {

		// Mode complet : Colonnes : SQLID, Query, Executed, Max, Avg, Total.
		// Allouer environ 60 % de la largeur au texte de la requête.
		queryWidth := int(float64(termWidth) * 0.6)
		if queryWidth < 40 {
			queryWidth = 40
		}

		// En-tête
		fmt.Printf("%s%-9s  %-*s  %10s  %12s  %12s  %12s%s\n",
			bold, "SQLID", queryWidth, "Query", "Executed", "Max", "Avg", "Total", reset)
		totalWidth := 9 + 2 + queryWidth + 2 + 10 + 2 + 12 + 2 + 12 + 2 + 12
		fmt.Println(strings.Repeat("-", totalWidth))

		// Affichage des top 10 requêtes
		for i, q := range queries {
			if i >= 10 {
				break
			}
			displayQuery := truncateQuery(q.Query, queryWidth)
			fmt.Printf("%-8s  %-*s  %10d  %12s  %12s  %12s\n",
				q.ID,
				queryWidth, displayQuery,
				q.Count,
				formatQueryDuration(q.MaxTime),
				formatQueryDuration(q.AvgTime),
				formatQueryDuration(q.TotalTime))
		}
	} else {
		// Mode simplifié : on fixe la largeur de la colonne "Type" à 10 caractères.
		// Colonnes fixes : SQLID (8), Type (10), Executed (10), Max (12), Avg (12), Total (12)
		// La largeur totale utilisée est donc : 8 + 2 + 10 + 2 + 10 + 2 + 12 + 2 + 12 + 2 + 12 = 80
		// (les espaces de séparation étant de 2 caractères chacune)
		header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s  %-12s\n", "SQLID", "Type", "Executed", "Max", "Avg", "Total")
		fmt.Print(bold + header + reset)
		fmt.Println(strings.Repeat("-", 80))
		for i, q := range queries {
			if i >= 10 {
				break
			}
			qType := queryTypeFromID(q.ID)
			fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s  %-12s\n",
				q.ID,
				qType,
				q.Count,
				formatQueryDuration(q.MaxTime),
				formatQueryDuration(q.AvgTime),
				formatQueryDuration(q.TotalTime))
		}
	}
}

// printSlowestQueries affiche les 10 requêtes individuelles les plus lentes,
// en affichant trois colonnes : SQLID, Query (tronquée) et Duration (durée moyenne).
func printSlowestQueries(queryStats map[string]*QueryStat) {
	// Structure locale pour rassembler les informations minimales.
	type queryInfo struct {
		ID      string
		Query   string
		MaxTime float64
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		// Générer l'identifiant "joli" pour la requête à partir de la requête brute et normalisée.
		id, _ := generateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			ID:      id,
			Query:   normalized,
			MaxTime: stats.MaxTime,
		})
	}

	// Tri décroissant par durée moyenne (AvgTime).
	sort.Slice(queries, func(i, j int) bool {
		return queries[i].MaxTime > queries[j].MaxTime
	})

	// Limiter l'affichage aux 10 premiers.
	if len(queries) > 10 {
		queries = queries[:10]
	}

	// Récupérer la largeur du terminal pour ajuster la largeur de la colonne Query.
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}
	// On réserve 9 colonnes pour SQLID et 12 colonnes pour Duration, plus des espaces de séparation.
	// La colonne Query aura donc une largeur maximum de : termWidth - (8 + 2 + 12 + 2 + 2*espaces supplémentaires)
	// Ici, nous fixons simplement une largeur minimale pour Query, par exemple 40 caractères.
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	// Préparer le style ANSI pour l'en-tête (facultatif, ici en gras)
	bold := "\033[1m"
	reset := "\033[0m"

	// En-tête du tableau.
	headerFormat := fmt.Sprintf("%%-9s  %%-%ds  %%12s\n", queryWidth)
	fmt.Printf("%s"+headerFormat+reset, bold, "SQLID", "Query", "Duration")
	totalWidth := 9 + 2 + queryWidth + 2 + 12
	fmt.Println(strings.Repeat("-", totalWidth))

	// Affichage des top 10 requêtes.
	for _, q := range queries {
		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Printf("%-9s  %-*s  %12s\n",
			q.ID,
			queryWidth, displayQuery,
			formatQueryDuration(q.MaxTime))
	}
}

// printMostFrequentQueries affiche le top des requêtes les plus fréquentes (triées par Count décroissant)
// en interrompant l'affichage si une requête a été exécutée une seule fois ou si le nombre d'exécutions chute de plus d'un facteur 10.
func printMostFrequentQueries(queryStats map[string]*QueryStat) {
	// Structure locale pour rassembler les informations minimales.
	type queryInfo struct {
		ID    string
		Query string
		Count int
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := generateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			ID:    id,
			Query: normalized,
			Count: stats.Count,
		})
	}

	// Tri décroissant par Count
	sort.Slice(queries, func(i, j int) bool {
		return queries[i].Count > queries[j].Count
	})

	// Récupérer la largeur du terminal pour ajuster la largeur de la colonne Query.
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}

	// On réserve 9 colonnes pour SQLID et 12 colonnes pour Duration, plus des espaces de séparation.
	// La colonne Query aura donc une largeur maximum de : termWidth - (8 + 2 + 12 + 2 + 2*espaces supplémentaires)
	// Ici, nous fixons simplement une largeur minimale pour Query, par exemple 40 caractères.
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	// Préparer le style ANSI pour l'en-tête (facultatif, ici en gras)
	bold := "\033[1m"
	reset := "\033[0m"

	// En-tête du tableau.
	headerFormat := fmt.Sprintf("%%-9s  %%-%ds  %%12s\n", queryWidth)
	fmt.Printf("%s"+headerFormat+reset, bold, "SQLID", "Query", "Executed")
	totalWidth := 9 + 2 + queryWidth + 2 + 12
	fmt.Println(strings.Repeat("-", totalWidth))

	// Affichage avec contrôle sur les conditions d'interruption
	var maxCount int
	var prevCount int
	for i, q := range queries {
		// Pour les 5 premières ligne, on l'affiche toujours
		if i == 0 {
			maxCount = prevCount // on retient la valeur max
			prevCount = q.Count
		} else {
			// Si la requête a été exécutée une seule fois, on s'arrête
			if q.Count == 1 {
				break
			}
			// Si le nombre d'exécutions chute de plus d'un facteur 10 par rapport à la ligne précédente, on s'arrête.
			if q.Count < prevCount/10 {
				break
			}
			// Si le nombre d'exécutions est inférieur à 1/2 moins 2 de la plus fréquente, on s'arrête
			if q.Count <= (maxCount/2)-2 {
				break
			}
			// Si déjà 15 lignes, on s'arrête
			if i == 15 {
				break
			}
			prevCount = q.Count
		}

		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Printf("%-9s  %-*s  %12d\n",
			q.ID,
			queryWidth, displayQuery,
			q.Count)
	}
}

// for smol terminal boi -- bouger dans output/text
func queryTypeFromID(id string) string {
	switch {
	case strings.HasPrefix(id, "se-"):
		return "select"
	case strings.HasPrefix(id, "in-"):
		return "insert"
	case strings.HasPrefix(id, "up-"):
		return "update"
	case strings.HasPrefix(id, "de-"):
		return "delete"
	case strings.HasPrefix(id, "co-"):
		return "copy"
	case strings.HasPrefix(id, "mv-"):
		return "refresh"
	default:
		return "other"
	}
}
