.PHONY: build generate clean test standalone

# Get current branch name (sanitized for filename)
BRANCH := $(shell git rev-parse --abbrev-ref HEAD | tr '/' '-')

# Build CLI binary (generates assets first)
build: generate
	go build -o bin/quellog_$(BRANCH) .

# Generate web assets (JS bundle)
generate:
	go generate ./web/...

# Build standalone HTML (WASM viewer)
standalone: generate
	go run web/standalone.go

# Run regression tests (golden file comparison)
test:
	go test ./test/... -v

# Clean generated files
clean:
	rm -f bin/quellog_$(BRANCH) web/app.bundle.js web/quellog.html
