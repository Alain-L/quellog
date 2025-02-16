package parser

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ParseAllFiles détecte le format pour chaque fichier et appelle le parser en streaming.
func ParseAllFiles(files []string, out chan<- LogEntry) {
	//defer close(out) // On ferme le canal après avoir traité tous les fichiers

	for _, file := range files {
		lp := detectParser(file)
		if lp == nil {
			log.Printf("[WARN] Unknown log format for file: %s\n", file)
			continue
		}
		err := lp.Parse(file, out)
		if err != nil {
			log.Printf("[ERROR] parse error for file %s: %v\n", file, err)
		}
	}
}

/*/ ParseAllFiles détecte le format pour chaque fichier et appelle le parser en streaming.
// Version parallélisée
func ParseAllFiles(files []string, out chan<- LogEntry) {
	var wg sync.WaitGroup

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()

			lp := detectParser(f)
			if lp == nil {
				log.Printf("[WARN] Format inconnu pour %s", f)
				return
			}

			if err := lp.Parse(f, out); err != nil {
				log.Printf("[ERROR] Erreur sur %s: %v", f, err)
			}
		}(file)
	}

	wg.Wait()
}
*/

// detectParser lit un petit bout du fichier pour identifier le format
func detectParser(filename string) LogParser {
	// 1) Vérifier si le fichier existe et n'est pas vide
	fi, err := os.Stat(filename)
	if err != nil {
		log.Printf("[ERROR] cannot stat file %s: %v", filename, err)
		return nil
	}
	if fi.Size() == 0 {
		log.Printf("[WARN] file %s is empty", filename)
		return nil
	}

	// 2) Ouvrir le fichier
	f, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] cannot open file %s: %v", filename, err)
		return nil
	}
	defer f.Close()

	// 3) Lire un buffer d'environ 4 KB
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		log.Printf("[ERROR] reading %s: %v", filename, err)
		return nil
	}
	if n == 0 {
		log.Printf("[WARN] no data could be read from %s", filename)
		return nil
	}

	sample := string(buf[:n])
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// 4) Détection basée sur l'extension + validation du contenu
	switch ext {
	case "json":
		if isJSONContent(sample) {
			return &JsonParser{}
		}
		log.Printf("[ERROR] file %s has .json extension but content is not valid JSON", filename)
		return nil

	case "csv":
		if isCSVContent(sample) {
			return &CsvParser{}
		}
		log.Printf("[ERROR] file %s has .csv extension but content is not valid CSV", filename)
		return nil

	case "log":
		if isLogContent(sample) {
			return &StderrParser{}
		}
		log.Printf("[ERROR] file %s has .log extension but content is not valid", filename)
		return nil

	default:
		// Détection basée sur le contenu pour les extensions inconnues
		switch {
		case isJSONContent(sample):
			log.Printf("[INFO] detected JSON format for unknown extension in %s", filename)
			return &JsonParser{}
		case isCSVContent(sample):
			log.Printf("[INFO] detected CSV format for unknown extension in %s", filename)
			return &CsvParser{}
		case strings.Contains(sample, "LOG:"):
			log.Printf("[INFO] detected LOG format for unknown extension in %s", filename)
			return &StderrParser{}
		default:
			log.Printf("[ERROR] unknown log format for file: %s", filename)
			return nil
		}
	}
}

// Helpers pour la validation de contenu
func isJSONContent(sample string) bool {
	trimmed := strings.TrimSpace(sample)
	if trimmed == "" {
		return false
	}
	if !(strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		return false
	}
	var js json.RawMessage
	return json.Unmarshal([]byte(trimmed), &js) == nil
}

func isCSVContent(sample string) bool {
	r := csv.NewReader(strings.NewReader(sample))
	if _, err := r.Read(); err != nil {
		return false
	}
	return true
}

// isLogContent vérifie si le contenu sample correspond à un format de log PostgreSQL (non JSON, non CSV).
// Il vérifie que la ligne commence par un timestamp (avec fuseau horaire facultatif) et contient un indicateur "LOG:" ou "ERROR:".
func isLogContent(sample string) bool {
	// regex
	// Autoriser un fuseau horaire après l'heure (ex: "CEST")
	dateTimeRegex := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}( [A-Z]+)?`)
	// Vérifier que la ligne contient "LOG:" ou "ERROR:" avec éventuellement des espaces après le deux-points
	logEntryRegex := regexp.MustCompile(`\b(LOG|ERROR):\s+`)

	// 3) Parcourir les lignes du sample
	lines := strings.Split(sample, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue // on ignore les lignes vides
		}
		// La ligne doit commencer par un timestamp et contenir un indicateur de log
		if dateTimeRegex.MatchString(trimmed) || logEntryRegex.MatchString(trimmed) {
			return true
		}
	}

	// 4) Si aucun motif n'est trouvé, le contenu n'est probablement pas un log PostgreSQL
	return false
}
