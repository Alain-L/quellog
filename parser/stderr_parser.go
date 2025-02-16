package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// StderrParser parse les logs PostgreSQL au format stderr
type StderrParser struct{}

// Parse prend un fichier et renvoie un canal de LogEntry en streaming.
func (p *StderrParser) Parse(filename string, out chan<- LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Optionnel : augmenter le buffer si nécessaire
	buf := make([]byte, 1024*1024)    // 1 MB
	scanner.Buffer(buf, 10*1024*1024) // jusqu'à 10 MB

	var currentEntry string

	for scanner.Scan() {
		line := scanner.Text()

		// Si la ligne commence par un espace ou une tabulation, c'est une continuation
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// On concatène avec un espace pour séparer proprement
			currentEntry += " " + strings.TrimSpace(line)
		} else {
			// Si currentEntry n'est pas vide, on la traite comme une entrée complète
			if currentEntry != "" {
				ts, msg := parseStderrLine(currentEntry)
				out <- LogEntry{Timestamp: ts, Message: msg}
			}
			// On démarre une nouvelle entrée avec la ligne actuelle
			currentEntry = line
		}
	}
	// Traiter la dernière entrée accumulée, s'il y en a une
	if currentEntry != "" {
		ts, msg := parseStderrLine(currentEntry)
		out <- LogEntry{Timestamp: ts, Message: msg}
	}

	return scanner.Err()
}

// parseStderrLine extrait le timestamp et le message d’une ligne de log stderr.
func parseStderrLine(line string) (time.Time, string) {
	// On suppose que la ligne commence par : "2024-06-05 00:00:01 CET ..."
	// On utilise strings.Fields pour découper par espaces (en supprimant les espaces multiples)
	parts := strings.Fields(line)
	if len(parts) < 4 {
		// Si la ligne n'a pas assez de champs, on retourne le line entier et un time.Time vide.
		return time.Time{}, line
	}
	// Combiner les trois premiers champs pour obtenir le timestamp complet.
	// Par exemple : "2024-06-05", "00:00:01", "CET"
	tsStr := fmt.Sprintf("%s %s %s", parts[0], parts[1], parts[2])
	// Utiliser le format correct avec fuseau horaire
	t, err := time.Parse("2006-01-02 15:04:05 MST", tsStr)
	if err != nil {
		// En cas d'erreur, on logge et on retourne un time.Time vide.
		return time.Time{}, line
	}
	// Le message est la suite de la ligne (à partir du 4e champ)
	msg := strings.Join(parts[3:], " ")
	return t, msg
}
