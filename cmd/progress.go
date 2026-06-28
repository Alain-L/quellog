// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
	"golang.org/x/term"
)

const (
	progressMinSize      = 500 * 1024 * 1024 // skip the bar below this total input size
	progressTickInterval = 40 * time.Millisecond
	barWidth             = 12
	emptyShade           = 240 // ANSI 256 grayscale, matches transitionShades[1]
)

// transitionShades maps a cell's in-cell sub-progress (1..7 eighths)
// to an ANSI 256-color grayscale value applied to ■. Capped at 250
// so the snap-to-default ■ at full reads as a step UP — values like
// 254 overshoot the typical terminal default fg (~252).
var transitionShades = [...]int{0, 240, 242, 244, 246, 248, 249, 250}

// progressBar streams a one-line indicator to stderr while a long
// parse runs. Only created when stderr is a TTY, --quiet is unset, and
// total input ≥ progressMinSize. The output format is irrelevant: the
// bar lives on stderr while the report goes to stdout or a file, so they
// never share a stream — and the bar prints only during the parse, then
// clears itself before any output. Outside those conditions
// newProgressBar returns nil; every method is a no-op on nil.
type progressBar struct {
	totalBytes int64
	bytesDone  atomic.Int64 // bytes from completed files
	startTime  time.Time
	stop       chan struct{}
	done       chan struct{}
}

// progressBarSuppressed reports whether the bar must stay off independently of
// the terminal: input too small to be worth it, or --quiet. Output format does
// NOT suppress it — the bar is stderr-only and clears before output, so it is
// safe (and useful) even for --html/--json/--yaml/--md.
func progressBarSuppressed(totalBytes int64) bool {
	return totalBytes < progressMinSize || quietFlag
}

func newProgressBar(totalBytes int64) *progressBar {
	if progressBarSuppressed(totalBytes) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	return &progressBar{
		totalBytes: totalBytes,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

func (p *progressBar) Start() {
	if p == nil {
		return
	}
	p.startTime = time.Now()
	go p.loop()
}

func (p *progressBar) AddBytes(n int64) {
	if p == nil {
		return
	}
	p.bytesDone.Add(n)
}

// Finish stops the redraw, waits for the goroutine to exit, then
// clears the line so the next stderr/stdout write starts fresh.
// Idempotent and nil-safe.
func (p *progressBar) Finish() {
	if p == nil {
		return
	}
	select {
	case <-p.stop:
		return
	default:
		close(p.stop)
	}
	<-p.done
	fmt.Fprint(os.Stderr, "\r\x1b[2K") // CSI 2K = erase entire line
}

func (p *progressBar) loop() {
	defer close(p.done)
	ticker := time.NewTicker(progressTickInterval)
	defer ticker.Stop()
	p.render()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.render()
		}
	}
}

// render writes the live line. Format mirrors the prefix of the
// final summary printed by PrintProcessingSummary so the eye reads
// the transition as one element settling:
//
//	live:  quellog v0.12.0 – ■■■■■□□□□□□□  41.7% – 216.00 MB/s
//	final: quellog v0.12.0 – 1597770 entries processed in 5.83 s (1.18 GB)
func (p *progressBar) render() {
	done := p.bytesDone.Load() + parser.CurrentFileProgress()
	if done > p.totalBytes {
		done = p.totalBytes
	}
	pct := 0.0
	if p.totalBytes > 0 {
		pct = float64(done) / float64(p.totalBytes) * 100
	}
	rate := float64(done) / time.Since(p.startTime).Seconds()
	fmt.Fprintf(os.Stderr,
		"\r\x1b[2Kquellog %s – %s %5.1f%% – %9s/s",
		version, renderBar(done, p.totalBytes, barWidth),
		pct, output.FormatBytes(int64(rate)),
	)
}

// renderBar draws a width-cell bar: ■ default for fully-reached
// cells, ■ at transitionShades[partial] for the active cell, □ at
// emptyShade for the rest. Monotonic per cell — each cell only ever
// brightens, no flicker.
func renderBar(done, total int64, width int) string {
	dimEmpty := func(n int) string {
		if n <= 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[0m", emptyShade, strings.Repeat("□", n))
	}
	if total <= 0 {
		return dimEmpty(width)
	}
	subUnits := int(float64(done) / float64(total) * float64(width) * 8)
	if subUnits > width*8 {
		subUnits = width * 8
	}
	full := subUnits / 8
	partial := subUnits % 8

	var b strings.Builder
	b.Grow(width*3 + 32)
	for i := 0; i < full; i++ {
		b.WriteString("■")
	}
	if partial > 0 && full < width {
		fmt.Fprintf(&b, "\x1b[38;5;%dm■\x1b[0m", transitionShades[partial])
		full++
	}
	b.WriteString(dimEmpty(width - full))
	return b.String()
}
