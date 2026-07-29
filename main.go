// Package main is the entry point for the quellog application.
// quellog is a PostgreSQL log parser and analyzer that provides
// detailed insights into database operations, performance, and events.
package main

import (
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"

	"github.com/Alain-L/quellog/cmd"
)

// Version information (set by goreleaser at build time)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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
	cmd.Execute(version, commit, date)
}
