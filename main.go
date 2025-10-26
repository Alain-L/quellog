// Package main is the entry point for the quellog application.
// quellog is a PostgreSQL log parser and analyzer that provides
// detailed insights into database operations, performance, and events.
package main

import (
	"dalibo/quellog/cmd"
)

func main() {
	// Execute the CLI application.
	// All command-line parsing, flag handling, and execution logic
	// is delegated to the cmd package.
	cmd.Execute()
}

// CPU profiling can be enabled for performance analysis:
//
// import (
//     "log"
//     "os"
//     "runtime/pprof"
// )
//
// f, err := os.Create("cpu.prof")
// if err != nil {
//     log.Fatal(err)
// }
// defer f.Close()
//
// if err := pprof.StartCPUProfile(f); err != nil {
//     log.Fatal(err)
// }
// defer pprof.StopCPUProfile()
//
// To analyze: go tool pprof cpu.prof
