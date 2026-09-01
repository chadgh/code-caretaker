# code-caretaker — see `make help`.

BINARY  := code-caretaker
PKGS    := ./...
CONFIG  ?= agent_loop.toml
GOFLAGS ?=
IMAGE   ?= ghcr.io/chadgh/code-caretaker
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/chadgh/code-caretaker/internal/cli.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into ./$(BINARY), stamped with $(VERSION).
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

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

.PHONY: hooks
hooks: ## Install the git hooks in .githooks/ (pre-commit runs `make verify`).
	git config core.hooksPath .githooks
	@echo "core.hooksPath -> .githooks"

.PHONY: hooks-uninstall
hooks-uninstall: ## Stop using .githooks/ and go back to .git/hooks/.
	@git config --unset core.hooksPath || true
	@echo "core.hooksPath unset"

.PHONY: run
run: ## Run the loop against this repo using $(CONFIG). Ctrl-C to stop.
	go run $(GOFLAGS) . --config $(CONFIG)

.PHONY: image
image: ## Build the container image as $(IMAGE):$(VERSION) for this machine.
	docker build --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

.PHONY: image-verify
image-verify: image ## Build the image and check its bundled tools are present.
	docker run --rm --entrypoint sh $(IMAGE):$(VERSION) -c \
		'set -e; id; git --version; gh --version | head -1; ssh -V; claude --version'
	docker run --rm $(IMAGE):$(VERSION) version

.PHONY: clean
clean: ## Remove build output.
	rm -f $(BINARY)
	go clean -testcache
	rm -rf .agent-status
