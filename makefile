.SUFFIXES:

SRCS := $(shell find ./cmd ./internal ./src -name '*.go' ! -name 'version.go')

# Lint tools run via `go run`, pinned so a new release cannot fail the build
# unannounced. staticcheck reads a toolchain's export data, so v0.8.x is the
# floor for the Go running here.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
DEADCODE    := golang.org/x/tools/cmd/deadcode@v0.48.0

help:  ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)} /^[a-zA-Z_.\/-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ build

.PHONY: build
build: ./bin/chisel-proxy  ## Build the binary into ./bin

./bin/chisel-proxy: $(SRCS) generate-version makefile go.mod go.sum
	mkdir -p ./bin
	go build -o ./bin/chisel-proxy ./cmd/chisel-proxy

# Best-effort: needs a git repo with at least one commit. A checked-in
# version.go keeps build/test working before the first commit.
.PHONY: generate-version
generate-version:
	@go run github.com/lczyk/version/go/cmd/generate-version -out ./src/version/version.go -pkg version 2>/dev/null \
		|| echo "generate-version: skipped (needs a git repo with a commit); keeping existing version.go"

.PHONY: install
install: ./bin/chisel-proxy  ## Symlink the binary into ~/.local/bin
	mkdir -p $(HOME)/.local/bin
	ln -sf "$(PWD)/bin/chisel-proxy" "$(HOME)/.local/bin/chisel-proxy"

##@ checks

.PHONY: test
test: generate-version  ## Run the unit tests with the race detector
	@if command -v gotest >/dev/null 2>&1; then \
		gotest -race ./...; \
	else \
		go test -race ./...; \
	fi

.PHONY: e2e
e2e: generate-version  ## Run the end-to-end test (needs `chisel` + network; override CHISEL=..)
	CHISEL_PROXY_E2E=1 go test -tags e2e -count=1 -v ./e2e/...

.PHONY: lint
lint:  ## go vet + staticcheck + deadcode + gofmt check (no writes)
	go vet ./...
	go run $(STATICCHECK) ./...
	@out=$$(go run $(DEADCODE) -test ./...); \
	if [ -n "$$out" ]; then echo "Unreachable code:"; echo "$$out"; exit 1; fi
	@out=$$(gofmt -s -l ./cmd ./internal ./src); \
	if [ -n "$$out" ]; then echo "Unformatted files:"; echo "$$out"; exit 1; fi

.PHONY: format
format:  ## gofmt the tree in place
	gofmt -s -w ./cmd ./internal ./src

.PHONY: cover
cover:  ## Coverage profile + HTML report (cover.out, cover.html)
	go test -coverpkg=./... -coverprofile=cover.out -race ./...
	go tool cover -func=cover.out
	go tool cover -html=cover.out -o cover.html

.PHONY: verify
verify: lint test  ## Pre-commit gate: lint + test
	@echo "All checks passed."

.PHONY: clean
clean:  ## Remove build artefacts and generated files
	rm -f ./bin/chisel-proxy
	rm -f ./src/version/version.go
	rm -f ./cover.out ./cover.html
