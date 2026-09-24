.PHONY: build generate clean test test-web wasm standalone

# Get current branch name (sanitized for filename)
BRANCH := $(shell git rev-parse --abbrev-ref HEAD | tr '/' '-')

# Build CLI binary (generates assets first)
build: generate
	go build -ldflags "-X main.version=$(VERSION)" -o bin/quellog_$(BRANCH) .

# Generate web assets (JS bundle)
generate:
	go generate ./web/...

# Build WASM module (requires tinygo + go@1.25 via brew)
# Version: exact tag if on a tag, otherwise latest-tag-dev. The latest tag is
# taken by version order, not by `git describe`: releases are tagged on main's
# merge commits, which dev never contains, so describe would fall back to an
# old tag that happens to be reachable.
LATEST_TAG := $(shell git tag --list 'v*' --sort=-v:refname | head -n 1)
VERSION := $(shell git describe --tags --exact-match 2>/dev/null || echo "$(or $(LATEST_TAG),dev)-dev")
GO125 := $(shell brew --prefix go@1.25 2>/dev/null)/bin
wasm: generate
	@# Inject version into source (tinygo ignores -X ldflags for wasm)
	sed -i '' 's/var version = ".*"/var version = "$(VERSION)"/' web/wasm/main.go
	PATH=$(GO125):$$PATH tinygo build -o web/quellog_tiny.wasm -target wasm -gc=leaking -no-debug ./web/wasm/main.go
	@# Restore source to default
	sed -i '' 's/var version = ".*"/var version = "dev"/' web/wasm/main.go

# Build standalone HTML (WASM viewer, requires tinygo)
standalone: wasm
	go run web/standalone.go

# Run regression tests (golden file comparison)
test:
	go test ./test/... -v

# Run web-layer tests (JS unit + contract linters + headless visual)
test-web:
	npm run test:web

# Clean generated files
clean:
	rm -f bin/quellog_$(BRANCH) web/app.bundle.js web/quellog_tiny.wasm web/quellog.html
