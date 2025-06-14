// parser/stderr_parser.go
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StderrParser parses PostgreSQL logs in stderr format.
type StderrParser struct{}

// Parse lit un fichier et envoie des LogEntry via le canal out, en utilisant un pool de workers.
func (p *StderrParser) Parse(filename string, out chan<- LogEntry) error {
	// Ouvrir le fichier.
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Augmenter la taille du buffer si nécessaire.
	buf := make([]byte, 4*1024*1024)   // 4 MB
	scanner.Buffer(buf, 100*1024*1024) // jusqu'à 100 MB

	// Channel pour transmettre les entrées complètes (messages multi-lignes).
	entriesChan := make(chan string, 1000)

	// Démarrage d'un pool de workers.
	numWorkers := 4 // ou ajuster en fonction des cœurs disponibles
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range entriesChan {
				timestamp, message := parseStderrLine(entry)
				out <- LogEntry{Timestamp: timestamp, Message: message}
			}
		}()
	}

	// Lecture du fichier ligne par ligne et assemblage des entrées multi-lignes.
	var currentEntry string
	for scanner.Scan() {
		line := scanner.Text()

		// Traitement spécifique aux logs syslog : couper avant le "#011".
		if idx := strings.Index(line, "#011"); idx != -1 {
			line = " " + line[idx+4:]
		}

		// Si la ligne commence par un espace ou une tabulation, c'est la suite de l'entrée précédente.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			currentEntry += " " + strings.TrimSpace(line)
		} else {
			// Si une entrée a été accumulée, l'envoyer au canal de travail.
			if currentEntry != "" {
				entriesChan <- currentEntry
			}
			// Commencer une nouvelle entrée.
			currentEntry = line
		}
	}
	// Envoyer la dernière entrée accumulée.
	if currentEntry != "" {
		entriesChan <- currentEntry
	}
	close(entriesChan) // signaler la fin des entrées

	// Attendre la fin de tous les workers.
	wg.Wait()

	return scanner.Err()
}

// parseStderrLine extrait le timestamp et le message d'une ligne de log stderr.
func parseStderrLine(line string) (time.Time, string) {
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return time.Time{}, line
	}

	// Format standard : "2006-01-02 15:04:05 MST"
	if len(parts[0]) == 10 && parts[0][4] == '-' && parts[0][7] == '-' {
		timestampStr := parts[0] + " " + parts[1] + " " + parts[2]
		if t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr); err == nil {
			return t, strings.Join(parts[3:], " ") // Rapide et optimisé
		}
	}

	// Format syslog : "Jan _2 15:04:05", ajout de l'année courante.
	currentYear := strconv.Itoa(time.Now().Year())
	timestampStr := currentYear + " " + parts[0] + " " + parts[1] + " " + parts[2]

	t, err := time.Parse("2006 Jan _2 15:04:05", timestampStr)
	if err == nil {
		return t, strings.Join(parts[3:], " ")
	}

	// Retourne la ligne brute si aucun format ne correspond.
	return time.Time{}, line
}
