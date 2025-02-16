package parser

import (
	"encoding/json"
	"os"
)

// JsonParser analyse les logs au format JSON
type JsonParser struct{}

func (p *JsonParser) Parse(filename string, out chan<- LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		close(out)
		return err
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	for decoder.More() {
		var entry LogEntry
		if err := decoder.Decode(&entry); err == nil {
			out <- entry
		}
	}
	close(out)

	return nil
}
