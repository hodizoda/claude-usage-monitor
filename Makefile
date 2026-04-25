BINARY := claude-usage-monitor

.PHONY: build install run preview once json clean fmt vet check help

build: ## Build the binary
	go build -o $(BINARY) .

install: ## Install to $GOBIN / $GOPATH/bin
	go install .

run: build ## Build and launch the TUI
	./$(BINARY)

preview: build ## Render one TUI frame to stdout (no alt-screen)
	./$(BINARY) --preview

once: build ## One-shot plain-text output
	./$(BINARY) --once

json: build ## One-shot JSON output
	./$(BINARY) --json

clean: ## Remove build artifacts
	rm -f $(BINARY)

fmt: ## Format Go source
	gofmt -w .

vet: ## Run go vet
	go vet ./...

check: fmt vet ## Format + vet
	@echo OK

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
