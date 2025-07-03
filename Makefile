.DEFAULT_GOAL := help

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: lint
lint:  ## Lint Go source files
	@go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@golangci-lint run -v -c .golangci.yaml ./...

.PHONY: lint-fix
lint-fix:  ## Lint Go source files and fix any errors
	@go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@golangci-lint run -v --fix -c .golangci.yaml ./...

.PHONY: test
test: ## Run unit tests
	go test -race \
			-coverpkg=./... \
			-coverprofile=coverageunit.out \
			-covermode=atomic \
			-count=1 \
			-timeout=5m \
			./...

.PHONY: bench
bench: ## Run benchmarks
	go test ./... -bench=. -run=XXX -benchmem -benchtime=1s -cpu 1