# code-caretaker — see `make help`.

BINARY  := code-caretaker
PKGS    := ./...
CONFIG  ?= agent_loop.toml
GOFLAGS ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the loop binary into ./$(BINARY).
	go build $(GOFLAGS) -o $(BINARY) .

.PHONY: test
test: ## Run the test suite.
	go test $(GOFLAGS) $(PKGS)

.PHONY: fmt
fmt: ## Format all Go source in place.
	gofmt -w -s .

.PHONY: fmt-check
fmt-check: ## Fail if any Go source needs formatting.
	@unformatted=$$(gofmt -l -s .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need 'make fmt':"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet.
	go vet $(PKGS)

.PHONY: verify
verify: fmt-check vet test build ## Everything CI should check: format, vet, test, build.

.PHONY: run
run: ## Run the loop against this repo using $(CONFIG). Ctrl-C to stop.
	go run $(GOFLAGS) . --config $(CONFIG)

.PHONY: clean
clean: ## Remove build output.
	rm -f $(BINARY)
	go clean -testcache
	rm -rf .agent-status
