// parser/stderr_parser.go
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// // Channel pour transmettre les entrées complètes (messages multi-lignes).
	// entriesChan := make(chan string, 1000)
	//
	// // Démarrage d'un pool de workers.
	// numWorkers := 4 // ou ajuster en fonction des cœurs disponibles
	// var wg sync.WaitGroup
	// for i := 0; i < numWorkers; i++ {
	// 	wg.Add(1)
	// 	go func() {
	// 		defer wg.Done()
	// 		for entry := range entriesChan {
	// 			timestamp, message := parseStderrLine(entry)
	// 			out <- LogEntry{Timestamp: timestamp, Message: message}
	// 		}
	// 	}()
	// }

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
				//entriesChan <- currentEntry
				timestamp, message := parseStderrLine(currentEntry)
				out <- LogEntry{Timestamp: timestamp, Message: message}
			}
			// Commencer une nouvelle entrée.
			currentEntry = line
		}
	}
	// // Envoyer la dernière entrée accumulée.
	// if currentEntry != "" {
	// 	entriesChan <- currentEntry
	// }
	// close(entriesChan) // signaler la fin des entrées
	//
	// // Attendre la fin de tous les workers.
	// wg.Wait()

	if currentEntry != "" {
		timestamp, message := parseStderrLine(currentEntry)
		out <- LogEntry{Timestamp: timestamp, Message: message}
	}

	return scanner.Err()
}

// fastParseStderrLine parse rapidement deux formats:
// 1) stderr: "2006-01-02 15:04:05 TZ <message...>"
// 2) syslog: "Jan _2 15:04:05 <rest...>" (année courante ajoutée)
func parseStderrLine(line string) (time.Time, string) {
	n := len(line)
	if n < 20 {
		return time.Time{}, line
	}

	// --- Tentative format stderr: "YYYY-MM-DD HH:MM:SS TZ ..."
	// Vérifs positionnelles minimales (dates/heures)
	if n >= 20 &&
		line[4] == '-' && line[7] == '-' &&
		line[10] == ' ' &&
		line[13] == ':' && line[16] == ':' {

		// s1 = index du 1er espace après la date (on sait que c'est 10)
		s1 := 10

		// s2 = index du 2e espace après l'heure
		// L'heure "HH:MM:SS" commence à s1+1 et dure 8 bytes donc espace attendu à 19.
		s2 := 19
		// Tolérance: si pas d'espace exact à 19 (espaces multiples/étranges), on rescanne.
		if line[s2] != ' ' {
			// scan pour trouver le prochain espace après l’heure
			i := s1 + 1 + 8
			for i < n && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			if i >= n {
				return time.Time{}, line
			}
			s2 = i
		}

		// Avancer au début du token timezone (skipper espaces/tabs)
		i := s2 + 1
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		tzStart := i

		// Trouver la fin du token timezone (jusqu'à espace/tab ou fin)
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		tzEnd := i

		if tzEnd > tzStart {
			// On a "YYYY-MM-DD HH:MM:SS <TZ>"
			tstr := line[:tzEnd]
			if t, err := time.Parse("2006-01-02 15:04:05 MST", tstr); err == nil {
				// Sauter les espaces avant le message
				for i < n && (line[i] == ' ' || line[i] == '\t') {
					i++
				}
				return t, line[i:]
			}
		}
		// Si la parse du timestamp échoue → on tentera fallback syslog plus bas
	}

	// --- Fallback format syslog: "Jan _2 15:04:05 ..."
	// Heuristique légère sur positions (mois + jour + heure):
	// "Jan _2 15:04:05" => indices 0..14 (longueur 15)
	if n >= 15 &&
		line[3] == ' ' && line[6] == ' ' &&
		line[9] == ':' && line[12] == ':' {

		// Construire "YYYY Jan _2 15:04:05"
		year := time.Now().Year()
		layout := "2006 Jan _2 15:04:05"
		// On parse uniquement les 15 premiers caractères (sans hostname/process, etc.)
		t, err := time.Parse(layout, fmt.Sprintf("%04d %s", year, line[:15]))
		if err == nil {
			// Le message commence après ces 15 chars (skipper espaces/tabs)
			i := 15
			for i < n && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			return t, line[i:]
		}
	}

	// Rien de reconnu proprement → renvoyer brut
	return time.Time{}, line
}

// parseStderrLine extrait le timestamp et le message d'une ligne de log stderr.
func parseStderrLineOld(line string) (time.Time, string) {
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
