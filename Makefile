.PHONY: build run tidy tf-init tf-apply clean help

# Variables
BINARY_NAME=gopher-ops
MAIN_PATH=cmd/main.go

help:
	@echo "🤖 Gopher-Ops Management Commands:"
	@echo "  make build      - Build the Go binary"
	@echo "  make run        - Run the bot directly"
	@echo "  make tidy       - Clean up Go dependencies"
	@echo "  make tf-init    - Initialize Terraform"
	@echo "  make tf-apply   - Deploy the infrastructure lab"
	@echo "  make clean      - Remove binary and state files"

build:
	go build -o $(BINARY_NAME) $(MAIN_PATH)

run:
	go run $(MAIN_PATH)

tidy:
	go mod tidy

tf-init:
	cd terraform && terraform init

tf-apply:
	cd terraform && terraform apply -auto-approve

clean:
	rm -f $(BINARY_NAME)
	rm -f state.json
	@echo "✅ Cleanup done."
