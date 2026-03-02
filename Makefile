.PHONY: build install clean test run-setup help

# Build the binary
build:
	@echo "Building keres..."
	@go build -o keres .
	@echo "✓ Build complete: ./keres"

# Build and install to GOPATH/bin
install:
	@echo "Installing keres..."
	@go install
	@echo "✓ Installed to: $$(go env GOPATH)/bin/keres"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -f keres
	@go clean
	@echo "✓ Clean complete"

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy
	@echo "✓ Dependencies ready"

# Run setup script
setup:
	@./setup.sh

# Run tests (when we add them)
test:
	@go test ./...

# Show help
help:
	@echo "Keres - Makefile targets:"
	@echo ""
	@echo "  make build    - Build the binary to ./keres"
	@echo "  make install  - Build and install to GOPATH/bin"
	@echo "  make clean    - Remove build artifacts"
	@echo "  make deps     - Download and tidy dependencies"
	@echo "  make setup    - Run the setup script"
	@echo "  make test     - Run tests"
	@echo "  make help     - Show this help message"
	@echo ""
	@echo "Quick start:"
	@echo "  1. make setup"
	@echo "  2. ./keres auth login"
	@echo "  3. ./keres gmail analyze"

# Default target
.DEFAULT_GOAL := help
