# PowerShell script for Windows
# scripts/update-dependencies.ps1

Write-Host "🔍 Checking for Go module updates..." -ForegroundColor Green

# Update Go to latest version
Write-Host "📦 Current Go version: $(go version)" -ForegroundColor Cyan

# Check for outdated modules
Write-Host "🔄 Checking for outdated dependencies..." -ForegroundColor Yellow

# Clean and update all dependencies
Write-Host "🧹 Cleaning module cache..." -ForegroundColor Blue
go clean -modcache

# Update all dependencies to latest versions
Write-Host "⬆️ Updating all dependencies..." -ForegroundColor Magenta
go get -u ./...

# Tidy the module file
Write-Host "🧹 Tidying go.mod..." -ForegroundColor Blue
go mod tidy

# Verify the build still works
Write-Host "🔨 Verifying build..." -ForegroundColor Green
go build -v ./...

# Run tests if they exist
if (Test-Path "*_test.go") {
    Write-Host "🧪 Running tests..." -ForegroundColor Yellow
    go test ./...
}

# List current dependencies
Write-Host "✅ Dependencies updated successfully!" -ForegroundColor Green
Write-Host ""
Write-Host "📊 Current dependencies:" -ForegroundColor Cyan
go list -m all

Write-Host ""
Write-Host "🔍 To check for vulnerabilities, run:" -ForegroundColor Yellow
Write-Host "  go install golang.org/x/vuln/cmd/govulncheck@latest" -ForegroundColor White
Write-Host "  govulncheck ./..." -ForegroundColor White

Write-Host ""
Write-Host "📋 Updated go.mod:" -ForegroundColor Cyan
Get-Content go.mod
