#!/bin/bash

# Keres Setup Script
# This script helps you get started with Keres

set -e

echo "Keres Setup"
echo "======================="
echo ""

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "Error: Go is not installed."
    echo ""
    echo "Please install Go 1.21 or higher from: https://golang.org/dl/"
    echo ""
    echo "After installing Go, run this setup script again."
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}')
echo "Go is installed: $GO_VERSION"
echo ""

# Download dependencies
echo "Downloading Go dependencies..."
go mod download
echo "Dependencies downloaded"
echo ""

# Build the binary
echo "Building keres..."
go build -o keres
echo "Built successfully: ./keres"
echo ""

# Create config directory
CONFIG_DIR="$HOME/.keres"
if [ ! -d "$CONFIG_DIR" ]; then
    mkdir -p "$CONFIG_DIR"
    echo "Created config directory: $CONFIG_DIR"
else
    echo "Config directory exists: $CONFIG_DIR"
fi
echo ""

# Check for credentials
CREDS_FILE="$CONFIG_DIR/credentials.json"
if [ ! -f "$CREDS_FILE" ]; then
    echo "Warning: Google OAuth2 credentials not found."
    echo ""
    echo "You need to set up Google API credentials to use Keres."
    echo ""
    echo "Follow these steps:"
    echo "1. Go to: https://console.cloud.google.com/"
    echo "2. Create a new project or select an existing one"
    echo "3. Enable Gmail API and Google Drive API"
    echo "4. Create OAuth2 credentials (Desktop app)"
    echo "5. Download the credentials JSON file"
    echo "6. Save it as: $CREDS_FILE"
    echo ""
    echo "See the README.md for detailed instructions."
    echo ""
else
    echo "Credentials file found: $CREDS_FILE"
    echo ""
fi

# Check if already authenticated
TOKEN_FILE="$CONFIG_DIR/token.json"
if [ -f "$TOKEN_FILE" ]; then
    echo "Already authenticated (token found)"
    echo ""
    echo "You can check your authentication status with:"
    echo "  ./keres auth status"
else
    echo "Not yet authenticated."
    echo ""
    if [ -f "$CREDS_FILE" ]; then
        echo "Ready to authenticate! Run:"
        echo "  ./keres auth login"
    fi
fi

echo ""
echo "Setup complete."
echo ""
echo "Quick start commands:"
echo "  ./keres auth login              # Authenticate with Google"
echo "  ./keres gmail analyze           # Analyze your Gmail"
echo "  ./keres drive analyze           # Analyze your Drive"
echo "  ./keres --help                  # See all commands"
echo ""
echo "For detailed usage, see: README.md"
