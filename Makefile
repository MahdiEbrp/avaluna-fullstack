
GO          ?= go
TAILWIND    ?= ./.cache/toolchain/tailwindcss
BIN         ?= bin/server
PKG         := ./...
CSS_IN      := web/static/css/input.css
CSS_OUT     := web/static/css/app.css

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## Download Go module dependencies
	$(GO) mod download

.PHONY: css
css: ## Build the Tailwind stylesheet (minified)
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --minify

.PHONY: css-watch
css-watch: ## Rebuild the stylesheet on change
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --watch

.PHONY: build
build: css ## Compile the server binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/server

.PHONY: run
run: ## Run the server (migrate + seed on first boot)
	$(GO) run ./cmd/server

.PHONY: fmt
fmt: ## Format every Go file
	gofmt -w cmd internal web

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## Run staticcheck (honnef.co/go/tools)
	staticcheck $(PKG)

.PHONY: test
test: ## Run the full test suite
	$(GO) test $(PKG)

.PHONY: cover
cover: ## Run tests with a coverage summary
	$(GO) test -cover ./internal/...

.PHONY: race
race: ## Run the suite with the race detector
	$(GO) test -race ./internal/...

.PHONY: stress
stress: ## Hammer the money, stock and auth paths from many goroutines
	$(GO) test -run TestStress -v ./internal/api/

.PHONY: stress-race
stress-race: ## Run the stress suite under the race detector
	$(GO) test -race -run TestStress ./internal/api/

.PHONY: check
check: fmt vet lint test ## Format, vet, lint and test — the full gate

.PHONY: check-all
check-all: fmt vet lint test stress-race ## The full gate plus the stress suite

.PHONY: reset
reset: ## Delete the SQLite database so the next boot re-seeds
	rm -f data/store.db data/store.db-wal data/store.db-shm

.PHONY: backup
backup: ## Snapshot the database into data/backups
	@mkdir -p data/backups
	@cp data/store.db data/backups/manual-$$(date +%Y%m%d-%H%M%S).db && echo "snapshot written"

.PHONY: routes
routes: ## Print the registered API route table (server must be running)
	@curl -s http://127.0.0.1:3000/api/v1/_routes | head -c 2000

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin $(CSS_OUT)

.PHONY: clean-runtime
clean-runtime: ## Remove generated runtime data (database, backups, invoices, uploads)
	rm -rf data/store.db data/store.db-wal data/store.db-shm \
	       data/backups data/invoices data/uploads
	@echo "runtime data cleared — the next boot will migrate and re-seed"
