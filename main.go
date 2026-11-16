// Package main is the entry point for the quellog application.
// quellog is a PostgreSQL log parser and analyzer that provides
// detailed insights into database operations, performance, and events.
package main

import (
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"strings"

	"github.com/Alain-L/quellog/cmd"
)

// Version information, set at build time by goreleaser and make. A plain
// go build or go install leaves it to fromBuildInfo.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// fromBuildInfo fills in the version information the build did not set,
// from what the Go toolchain records in the binary: the module version (a
// tag, or a pseudo-version between tags, "+dirty" for a modified tree) and
// the commit and its time. Without that record it leaves the defaults.
func fromBuildInfo() {
	if version != "dev" {
		return
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		version = strings.TrimPrefix(v, "v")
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			date = s.Value
		}
	}
}

func main() {

	// CPU profiling
	if cpuProfile := os.Getenv("CPUPROFILE"); cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	// Execution tracing (go tool trace) — investigation hook, same
	// contract as CPUPROFILE/MEMPROFILE.
	if traceFile := os.Getenv("TRACEPROFILE"); traceFile != "" {
		f, err := os.Create(traceFile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := trace.Start(f); err != nil {
			log.Fatal(err)
		}
		defer trace.Stop()
	}

	// Block profiling (where goroutines block on channels/mutexes) —
	// investigation hook; inflates wall, read only the blocking distribution.
	if blockProfile := os.Getenv("BLOCKPROFILE"); blockProfile != "" {
		runtime.SetBlockProfileRate(1)
		f, err := os.Create(blockProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer func() {
			_ = pprof.Lookup("block").WriteTo(f, 0)
			f.Close()
		}()
	}

	// Memory profiling
	if memProfile := os.Getenv("MEMPROFILE"); memProfile != "" {
		f, err := os.Create(memProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer func() {
			pprof.WriteHeapProfile(f)
			f.Close()
		}()
	}

	// Execute the CLI application.
	// All command-line parsing, flag handling, and execution logic
	// is delegated to the cmd package.
	fromBuildInfo()
	cmd.Execute(version, commit, date)
}
