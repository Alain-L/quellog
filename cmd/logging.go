// Package cmd contains the logging setup for quellog.
//
// We use slog with a small custom handler that produces the same
// "[LEVEL] message" output the project has used historically with
// log.Printf("[INFO] ..."). This keeps user-facing output stable while
// giving us level filtering, a single source of truth for warn/error
// formatting, and a path toward structured/JSON output later.
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// prettyHandler renders a slog.Record as "[LEVEL] message key=value..."
// followed by a newline. Attributes are appended in the order they were
// added; groups are flattened into "group.key=value".
type prettyHandler struct {
	mu     *sync.Mutex
	out    io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

func newPrettyHandler(out io.Writer, level slog.Leveler) *prettyHandler {
	return &prettyHandler{
		mu:    &sync.Mutex{},
		out:   out,
		level: level,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", r.Level.String(), r.Message)

	prefix := strings.Join(h.groups, ".")
	for _, a := range h.attrs {
		writeAttr(&b, prefix, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, prefix, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	fmt.Fprintf(b, " %s=%v", key, a.Value.Any())
}

// logLevel is a mutable slog.Leveler so the active level can change
// after flag parsing. Initialised to Info; --quiet bumps it to Warn,
// --verbose (future) would lower it to Debug.
var logLevel = new(slog.LevelVar) // zero value = LevelInfo

// initLogger installs the pretty handler as slog's default. The active
// level is read from logLevel each time a record is emitted, so callers
// can flip it (e.g. via --quiet) after Execute() has run.
func initLogger() {
	slog.SetDefault(slog.New(newPrettyHandler(os.Stderr, logLevel)))
}

// setLogLevel changes the package-level log filter at runtime.
// Call this from a Cobra PersistentPreRun once the --quiet/--verbose
// flags are bound and parsed.
func setLogLevel(level slog.Level) {
	logLevel.Set(level)
}
