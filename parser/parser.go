// parser/parser.go
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

const timeLayout = "2006-01-02 15:04:05" // adapter le layout si nécessaire

// ParseLine tente d'extraire le timestamp et le message d'une ligne de log.
func ParseLine(line string) (LogEntry, error) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return LogEntry{}, fmt.Errorf("ligne trop courte : %s", line)
	}
	tsStr := parts[0] + " " + parts[1]
	t, err := time.Parse(timeLayout, tsStr)
	if err != nil {
		return LogEntry{}, err
	}
	// Le reste de la ligne est considéré comme message
	message := ""
	if len(parts) == 3 {
		message = parts[2]
	}
	return LogEntry{Timestamp: t, Message: message}, nil
}

// ParseLogFile lit un fichier et retourne la liste des entrées valides.
func ParseLogFile(filename string) ([]LogEntry, error) {
	var entries []LogEntry

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("ouverture du fichier %s échouée : %w", filename, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		entry, err := ParseLine(line)
		if err != nil {
			// On peut choisir d'ignorer ou de logger l'erreur de parsing
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("erreur de lecture du fichier %s : %w", filename, err)
	}
	return entries, nil
}
