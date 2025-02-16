package parser

import "time"

// LogEntry représente un log PostgreSQL standardisé.
type LogEntry struct {
	Timestamp time.Time
	Message   string
}

// LogParser est l'interface que tous les parsers doivent implémenter.
// Ici : signature pour le "streaming" via un canal.
type LogParser interface {
	Parse(filename string, out chan<- LogEntry) error
}
