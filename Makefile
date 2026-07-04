.PHONY: all test test-mcpgo test-officialsdk fmt fmt-check vet build build-examples tidy clean coverage check lint help run-examples stop-examples install-hooks

MODULES := . ./mcpgo ./officialsdk \
	./examples/mcpgo/basic ./examples/mcpgo/advanced \
	./examples/officialsdk/basic ./examples/officialsdk/advanced

# Default target
all: fmt vet test

# Run all tests
test:
	@echo "Running tests..."
	@go test -v -race ./...

# Run mcpgo tests only
test-mcpgo:
	@echo "Running mcpgo tests..."
	@cd mcpgo && go test -v -race ./...

# Run officialsdk tests only
test-officialsdk:
	@echo "Running officialsdk tests..."
	@cd officialsdk && go test -v -race ./...

# Run tests with coverage (merges profiles across all workspace modules)
coverage:
	@echo "Running tests with coverage..."
	@mkdir -p .coverage
	@for mod in $(MODULES); do \
		name=$$(echo $$mod | tr '/' '-' | tr '.' 'root'); \
		(cd $$mod && go test -race -coverprofile=coverage.out ./... 2>/dev/null) && \
		mv $$mod/coverage.out .coverage/$$name.out 2>/dev/null; \
	done; true
	@echo 'mode: atomic' > coverage.out
	@tail -q -n +2 .coverage/*.out >> coverage.out 2>/dev/null || true
	@go tool cover -html=coverage.out -o coverage.html
	@rm -rf .coverage
	@echo "Coverage report generated: coverage.html"

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Check for formatting issues
fmt-check:
	@echo "Checking code formatting..."
	@test -z "$$(gofmt -l .)" || (echo "The following files need formatting:" && gofmt -l . && exit 1)

# Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

# Build the project
build:
	@echo "Building..."
	@go build ./...

# Build example binaries into their respective directories
build-examples:
	@echo "Building examples..."
	@for d in examples/*/*; do \
		echo "  $$d"; \
		(cd "$$d" && go build -o "$$(basename $$d)" .); \
	done

# Tidy up dependencies across all workspace modules
tidy:
	@echo "Tidying dependencies..."
	@for mod in $(MODULES); do \
		echo "  tidying $$mod"; \
		(cd $$mod && go mod tidy); \
	done

# Run golangci-lint
lint:
	@echo "Running linter..."
	@golangci-lint run ./...

# Clean build artifacts and coverage files
clean:
	@echo "Cleaning..."
	@rm -f coverage.out coverage.html
	@for d in examples/*/*; do rm -f "$$d/$$(basename $$d)"; done
	@go clean

# Run all checks (formatting, vetting, testing)
check: fmt-check vet test
	@echo "All checks passed!"

# Run all example servers in the background
run-examples: build-examples
	@echo "Starting all example servers..."
	@MCPCAT_PROJECT_ID=$${MCPCAT_PROJECT_ID:-proj_YOUR_PROJECT_ID} examples/mcpgo/basic/basic & echo "  mcpgo-basic        (pid $$!) → http://localhost:8081/mcp"
	@MCPCAT_PROJECT_ID=$${MCPCAT_PROJECT_ID:-proj_YOUR_PROJECT_ID} examples/mcpgo/advanced/advanced & echo "  mcpgo-advanced     (pid $$!) → http://localhost:8082/mcp"
	@MCPCAT_PROJECT_ID=$${MCPCAT_PROJECT_ID:-proj_YOUR_PROJECT_ID} examples/officialsdk/basic/basic & echo "  officialsdk-basic  (pid $$!) → http://localhost:8083/mcp"
	@MCPCAT_PROJECT_ID=$${MCPCAT_PROJECT_ID:-proj_YOUR_PROJECT_ID} examples/officialsdk/advanced/advanced & echo "  officialsdk-advanced (pid $$!) → http://localhost:8084/mcp"
	@echo "All servers started. Use 'make stop-examples' to stop them."

# Stop all example servers (by the ports they listen on)
stop-examples:
	@echo "Stopping example servers..."
	@-kill $$(lsof -ti:8081) 2>/dev/null
	@-kill $$(lsof -ti:8082) 2>/dev/null
	@-kill $$(lsof -ti:8083) 2>/dev/null
	@-kill $$(lsof -ti:8084) 2>/dev/null
	@echo "Done."

# Install git hooks
install-hooks:
	@git config core.hooksPath hooks
	@echo "Git hooks installed (using hooks/ directory)."

# Show help
help:
	@echo "Available targets:"
	@echo "  make all              - Format, vet, and test (default)"
	@echo "  make test             - Run all tests with race detection"
	@echo "  make test-mcpgo       - Run mcpgo tests only"
	@echo "  make test-officialsdk - Run officialsdk tests only"
	@echo "  make coverage         - Run tests with coverage report"
	@echo "  make fmt              - Format all Go files"
	@echo "  make fmt-check        - Check if files need formatting (CI mode)"
	@echo "  make vet              - Run go vet"
	@echo "  make lint             - Run golangci-lint"
	@echo "  make build            - Build the project"
	@echo "  make build-examples   - Build example binaries in place"
	@echo "  make tidy             - Tidy dependencies (all modules)"
	@echo "  make clean            - Remove build artifacts"
	@echo "  make check            - Run all checks (format check, vet, test)"
	@echo "  make run-examples     - Build & start all example servers"
	@echo "  make stop-examples    - Stop all example servers"
	@echo "  make install-hooks    - Install git hooks from hooks/ directory"
	@echo "  make help             - Show this help message"
