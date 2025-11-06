// Package main is the entry point for the quellog application.
// quellog is a PostgreSQL log parser and analyzer that provides
// detailed insights into database operations, performance, and events.
package main

import (
	"log"
	"os"
	"runtime/pprof"

	"github.com/Alain-L/quellog/cmd"
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
	cmd.Execute()
}
