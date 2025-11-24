package parser

import (
	"os"
	"strings"
	"testing"
)

// Benchmark current detection method (regex-based)
func BenchmarkCurrentDetection(b *testing.B) {
	// Load real log sample
	data, err := os.ReadFile("../_random_logs/samples/B.log")
	if err != nil {
		b.Skip("Sample log not available")
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 100 {
		lines = lines[:100] // Use first 100 lines
	}
	sample := strings.Join(lines, "\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isLogContent(sample)
	}
}

// Benchmark new detection method (AnalyzePrefixes)
func BenchmarkNewDetection(b *testing.B) {
	// Load real log sample
	data, err := os.ReadFile("../_random_logs/samples/B.log")
	if err != nil {
		b.Skip("Sample log not available")
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 100 {
		lines = lines[:100]
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AnalyzePrefixes(lines, 20)
	}
}

// Benchmark with small sample (20 lines)
func BenchmarkCurrentDetectionSmall(b *testing.B) {
	data, err := os.ReadFile("../_random_logs/samples/B.log")
	if err != nil {
		b.Skip("Sample log not available")
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}
	sample := strings.Join(lines, "\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isLogContent(sample)
	}
}

func BenchmarkNewDetectionSmall(b *testing.B) {
	data, err := os.ReadFile("../_random_logs/samples/B.log")
	if err != nil {
		b.Skip("Sample log not available")
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AnalyzePrefixes(lines, 20)
	}
}

// Benchmark overhead: current vs new on typical detection scenario
func BenchmarkDetectionOverhead(b *testing.B) {
	data, err := os.ReadFile("../_random_logs/samples/B.log")
	if err != nil {
		b.Skip("Sample log not available")
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 32 {
		lines = lines[:32] // Typical sample size for detection
	}

	b.Run("Current_Regex", func(b *testing.B) {
		sample := strings.Join(lines, "\n")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = isLogContent(sample)
		}
	})

	b.Run("New_AnalyzePrefixes", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = AnalyzePrefixes(lines, 20)
		}
	})
}
