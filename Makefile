.PHONY: help build lint vet test fmt coverage changelog hooks clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build dev binary (./exo-teams)
	go build -o exo-teams ./cmd/exo-teams/

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

test: ## Run tests with race detection
	go test -race ./...

fmt: ## Format all Go files
	gofmt -w .

coverage: ## Run tests with coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	@rm -f coverage.out

changelog: ## Generate changelog from commits
	@command -v git-cliff > /dev/null 2>&1 && git-cliff -o CHANGELOG.md || echo "install git-cliff: cargo install git-cliff"

hooks: ## Install git hooks
	@if [ -d .git ]; then cp scripts/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit && echo "pre-commit hook installed"; fi

clean: ## Clean build artifacts
	rm -f exo-teams coverage.out
	rm -rf dist/
