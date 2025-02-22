package parser

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
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

// detectParser lit un petit bout du fichier pour identifier le format
func detectParser(filename string) LogParser {
	// 1) Vérifier si le fichier existe et n'est pas vide.
	fi, err := os.Stat(filename)
	if err != nil {
		log.Printf("[ERROR] cannot stat file %s: %v", filename, err)
		return nil
	}
	if fi.Size() == 0 {
		log.Printf("[WARN] file %s is empty", filename)
		return nil
	}

	// 2) Ouvrir le fichier.
	f, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] cannot open file %s: %v", filename, err)
		return nil
	}
	defer f.Close()

	// Convertir en string en s'assurant de ne pas couper une ligne au milieu
	buf := make([]byte, 32768) // Crée un buffer de 32 Ko
	n, err := f.Read(buf)      // Lit jusqu'à 32 Ko dans buf
	rawsample := string(buf[:n])
	lastNewline := strings.LastIndex(rawsample, "\n")
	if lastNewline == -1 {
		// Cas rare : aucune nouvelle ligne dans les 32 Ko
		// On étend la lecture jusqu'à trouver 5 '\n' ou la fin du fichier
		extendedSample, err := readUntilNLines(f, 5)
		if err != nil {
			log.Printf("[ERROR] extending read for %s: %v", filename, err)
			return nil
		}
		if extendedSample == "" {
			log.Printf("[WARN] no newline found in extended read for %s", filename)
			return nil
		}
		rawsample = extendedSample
		lastNewline = len(rawsample) - 1 // Le dernier caractère est maintenant un '\n'
	}

	// Delete last incomplete line
	sample := rawsample[:lastNewline]

	// Check if binary file
	if isBinaryContent(sample) {
		log.Printf("[ERROR] %s is binary. quellog does not support this format yet.", filename)
		return nil
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// 4) Détection basée sur l'extension + validation du contenu.
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
		// Détection basée sur le contenu pour les extensions inconnues.
		switch {
		case isJSONContent(sample):
			log.Printf("[INFO] detected JSON format for unknown extension in %s", filename)
			return &JsonParser{}
		case isCSVContent(sample):
			log.Printf("[INFO] detected CSV format for unknown extension in %s", filename)
			return &CsvParser{}
		case isLogContent(sample):
			log.Printf("[INFO] detected LOG format for unknown extension in %s", filename)
			return &StderrParser{}
		default:
			log.Printf("[ERROR] unknown log format for file: %s", filename)
			return nil
		}
	}
}

// Helpers

// readUntilNLines lit le fichier jusqu'à trouver n lignes ou la fin du fichier
func readUntilNLines(f *os.File, n int) (string, error) {
	var (
		sample    strings.Builder
		lineCount int
	)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		sample.WriteString(scanner.Text() + "\n") // Ajouter le saut de ligne
		lineCount++
		if lineCount >= n {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return sample.String(), nil
}

// isJSONContent verifies whether the given sample appears to be valid JSON log content.
// It checks that the sample is not empty, starts with '{' or '[',
// and, if it's an object (starting with '{'), that it contains a "timestamp" or "insertId" field.
func isJSONContent(sample string) bool {
	trimmed := strings.TrimSpace(sample)
	if trimmed == "" {
		return false
	}
	// Must start with '{' or '['.
	if !(strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		return false
	}

	// If it starts with '{', ensure it contains a key "timestamp" or "insertId".
	if strings.HasPrefix(trimmed, "{") {
		// This regex checks if the JSON object starts with a key "timestamp" or "insertId"
		re := regexp.MustCompile(`^\s*\{\s*"(timestamp|insertId)"\s*:`)
		if !re.MatchString(trimmed) {
			return false
		}
	}

	// Finally, try to unmarshal the JSON.
	var js interface{}
	return json.Unmarshal([]byte(trimmed), &js) == nil
}

// isCSVContent verifies whether the given sample appears to be a valid CSV log.
// It checks that the sample contains a minimum number of commas,
// that parsing returns enough fields, and that the first field resembles a timestamp.
func isCSVContent(sample string) bool {
	// Check that the sample contains at least 12 commas (commonly used heuristic).
	if strings.Count(sample, ",") < 12 {
		return false
	}

	// Use csv.Reader to parse the sample.
	r := csv.NewReader(strings.NewReader(sample))
	record, err := r.Read()
	if err != nil {
		return false
	}

	// Ensure that the record contains at least 12 fields.
	if len(record) < 12 {
		return false
	}

	// Check if the first field resembles a timestamp.
	// Expected format: "YYYY-MM-DD HH:MM:SS" with optional fractional seconds and an optional timezone.
	dateRegex := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?(?: [A-Z]{2,5})?$`)
	if !dateRegex.MatchString(strings.TrimSpace(record[0])) {
		return false
	}

	return true
}

// isLogContent verifies whether the given sample appears to be a PostgreSQL log in a syslog/stderr style.
func isLogContent(sample string) bool {

	// Define a slice of regex patterns covering common variations for stderr/syslog logs.
	patterns := []*regexp.Regexp{

		// Pattern 1: ISO-style timestamp
		// - ISO-style timestamp with 'T' or space,
		// - optional fraction,
		// - optional timezone,
		// - followed by any text,
		// - then a log level indicator.
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?: [A-Z]{2,5})?.*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 2: (Syslog)
		// - abbreviated day (e.g., "Mon  3 12:34:56"),
		// - optional fraction,
		// - optional TZ,
		// - followed by host and program info,
		// then a log level indicator.
		regexp.MustCompile(`^[A-Z][a-z]{2}\s+\d+\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:\s+[A-Z]{2,5})?\s+\S+\s+\S+\[\d+\]:(?:\s+\[[^\]]+\])?\s+\[\d+(?:-\d+)?\].*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 3:
		//  - epoch timestamp or date-time (without 'T')
		//  - followed by a log level indicator.
		regexp.MustCompile(`^(?:\d{10}\.\d{3}|\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}).*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 4: (Minimal)
		// - a date-time (ISO or space separated)
		// - followed by any text and one of the basic levels.
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}.*?\b(?:LOG|ERROR|WARNING|FATAL|PANIC):\s+`),
	}

	// Split the sample into lines.
	lines := strings.Split(sample, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Check each pattern: if any matches, consider the sample as a valid log.
		for _, re := range patterns {
			if re.MatchString(trimmed) {
				return true
			}
		}
	}
	return false
}

// isBinaryContent checks if the sample contains non-printable characters or null bytes,
// which indicates that the file is likely in a binary format.
func isBinaryContent(sample string) bool {
	// Check for null characters.
	if strings.Contains(sample, "\x00") {
		return true
	}
	// Count non-printable characters (excluding common whitespace like \n, \r, \t).
	nonPrintable := 0
	for _, r := range sample {
		// Consider ASCII control characters (below 32) except newline, carriage return, and tab.
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			nonPrintable++
		}
	}
	// If more than 30% of the characters are non-printable, consider the sample binary.
	if float64(nonPrintable) > float64(len(sample))*0.3 {
		return true
	}
	return false
}
