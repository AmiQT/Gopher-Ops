.PHONY: help build run tidy fmt lint test cover test-integration tf-init tf-apply clean

BINARY_NAME=gopher-ops
MAIN_PATH=./cmd
COVER_FILE=coverage.out
COVER_MIN=60

help:  ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

build:  ## Build the Go binary
	go build -o $(BINARY_NAME) $(MAIN_PATH)

run:  ## Run the bot directly
	go run $(MAIN_PATH)

tidy:  ## Sync go.mod and go.sum
	go mod tidy

fmt:  ## Format all Go code
	gofmt -s -w .

lint:  ## Run golangci-lint
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint not installed — run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi
	golangci-lint run ./...

test:  ## Run unit tests
	go test -race -timeout 120s ./...

cover:  ## Generate coverage report + enforce min threshold
	go test -race -coverprofile=$(COVER_FILE) -covermode=atomic ./...
	@go tool cover -func=$(COVER_FILE) | tail -1
	@COVERAGE=$$(go tool cover -func=$(COVER_FILE) | grep total | awk '{print substr($$3, 1, length($$3)-1)}'); \
	echo "Coverage: $$COVERAGE% (min: $(COVER_MIN)%)"; \
	awk -v cov="$$COVERAGE" -v min="$(COVER_MIN)" 'BEGIN { if (cov+0 < min+0) { print "❌ Coverage below threshold"; exit 1 } else { print "✅ Coverage OK" } }'

test-integration:  ## Run integration tests (requires Docker)
	go test -race -tags=integration -timeout 5m ./...

tf-init:  ## Initialize Terraform
	cd terraform && terraform init

tf-apply:  ## Deploy the infrastructure lab
	cd terraform && terraform apply -auto-approve

clean:  ## Remove binary and state files
	rm -f $(BINARY_NAME) $(COVER_FILE) state.json
	@echo "✅ Cleanup done."
