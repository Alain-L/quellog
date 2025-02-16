package parser

import (
	"encoding/csv"
	"os"
	"time"
)

// CsvParser analyse les logs CSV
type CsvParser struct{}

func (p *CsvParser) Parse(filename string, out chan<- LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		close(out)
		return err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, _ := reader.ReadAll()

	for _, r := range records {
		timestamp, _ := time.Parse("2006-01-02 15:04:05", r[0])
		out <- LogEntry{Timestamp: timestamp, Message: r[1]}
	}
	close(out)

	return nil
}
