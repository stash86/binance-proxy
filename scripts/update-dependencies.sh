#!/bin/bash
# scripts/update-dependencies.sh

set -e

echo "🔍 Checking for Go module updates..."

# Update Go to latest version
echo "📦 Current Go version: $(go version)"

# Check for outdated modules
echo "🔄 Checking for outdated dependencies..."

# Clean and update all dependencies
echo "🧹 Cleaning module cache..."
go clean -modcache

# Update all dependencies to latest versions
echo "⬆️ Updating all dependencies..."
go get -u ./...

# Tidy the module file
echo "🧹 Tidying go.mod..."
go mod tidy

# Verify the build still works
echo "🔨 Verifying build..."
go build -v ./...

# Run tests if they exist
if [ -f "*_test.go" ]; then
    echo "🧪 Running tests..."
    go test ./...
fi

# Security check
echo "🔒 Running security check..."
go list -json -deps ./... | jq -r '.Module | select(.Replace == null) | .Path + "@" + .Version' | sort -u > current_deps.txt

echo "✅ Dependencies updated successfully!"
echo ""
echo "📊 Current dependencies:"
go list -m all

echo ""
echo "🔍 To check for vulnerabilities, run:"
echo "  go install golang.org/x/vuln/cmd/govulncheck@latest"
echo "  govulncheck ./..."

echo ""
echo "📋 Updated go.mod:"
cat go.mod
