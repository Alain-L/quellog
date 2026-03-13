//go:build ignore

// Bundle web assets using esbuild Go API.
// Run with: go run scripts/bundle.go
// Or via: go generate ./web/...
package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	// Bundle ES modules into a single IIFE file (minified for embedding)
	fmt.Println("Bundling JS modules...")
	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{"web/app.js"},
		Bundle:            true,
		Outfile:           "web/app.bundle.js",
		Format:            api.FormatIIFE,
		Platform:          api.PlatformBrowser,
		Target:            api.ES2020,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Write:             true,
		LogLevel:          api.LogLevelInfo,
	})

	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Text)
		}
		os.Exit(1)
	}

	fmt.Printf("  web/app.bundle.js (%d bytes)\n", len(result.OutputFiles[0].Contents))
	fmt.Println("\nDone!")
}
