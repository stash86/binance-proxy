package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DependencyInfo represents information about a Go module dependency
type DependencyInfo struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Time     string `json:"Time"`
	Update   string `json:"Update,omitempty"`
	Indirect bool   `json:"Indirect"`
}

// VulnerabilityInfo represents security vulnerability information
type VulnerabilityInfo struct {
	ID          string `json:"id"`
	Package     string `json:"package"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Fixed       string `json:"fixed,omitempty"`
}

func main() {
	fmt.Println("🔍 Binance Proxy Dependency Analysis Tool")
	fmt.Println("=========================================")

	if len(os.Args) > 1 && os.Args[1] == "--help" {
		printUsage()
		return
	}

	// Check if we're in a Go module directory
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		fmt.Println("❌ Error: Not in a Go module directory (go.mod not found)")
		os.Exit(1)
	}

	// Analyze current dependencies
	analyzeDependencies()

	// Check for updates
	checkForUpdates()

	// Security scan
	runSecurityScan()

	// Generate recommendations
	generateRecommendations()
}

func printUsage() {
	fmt.Println(`
Usage: go run scripts/dependency-analyzer.go [options]

Options:
  --help    Show this help message

This tool analyzes Go module dependencies for:
- Current versions and update availability
- Security vulnerabilities
- Indirect dependencies
- Recommendations for updates

Example:
  go run scripts/dependency-analyzer.go
`)
}

func analyzeDependencies() {
	fmt.Println("\n📦 Current Dependencies Analysis")
	fmt.Println("--------------------------------")

	// Get list of all dependencies
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("❌ Error getting dependencies: %v\n", err)
		return
	}

	// Parse dependencies
	dependencies := parseDependencies(string(output))

	fmt.Printf("📊 Total dependencies: %d\n", len(dependencies))

	directCount := 0
	indirectCount := 0

	for _, dep := range dependencies {
		if dep.Indirect {
			indirectCount++
		} else {
			directCount++
		}
	}

	fmt.Printf("   - Direct: %d\n", directCount-1) // -1 to exclude main module
	fmt.Printf("   - Indirect: %d\n", indirectCount)

	fmt.Println("\n🔍 Direct Dependencies:")
	for _, dep := range dependencies {
		if !dep.Indirect && dep.Path != "binance-proxy" {
			age := calculateAge(dep.Time)
			fmt.Printf("   %-40s %s (%s)\n", dep.Path, dep.Version, age)
		}
	}
}

func checkForUpdates() {
	fmt.Println("\n🔄 Checking for Updates")
	fmt.Println("-----------------------")

	// Check for module updates
	cmd := exec.Command("go", "list", "-u", "-m", "all")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("❌ Error checking updates: %v\n", err)
		return
	}

	lines := strings.Split(string(output), "\n")
	updatesAvailable := false

	for _, line := range lines {
		if strings.Contains(line, "[") && strings.Contains(line, "]") {
			updatesAvailable = true
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				module := parts[0]
				versions := parts[1]
				if strings.Contains(versions, "[") {
					fmt.Printf("   ⬆️  %s %s\n", module, versions)
				}
			}
		}
	}

	if !updatesAvailable {
		fmt.Println("   ✅ All dependencies are up to date!")
	}
}

func runSecurityScan() {
	fmt.Println("\n🔒 Security Vulnerability Scan")
	fmt.Println("------------------------------")

	// Check if govulncheck is installed
	_, err := exec.LookPath("govulncheck")
	if err != nil {
		fmt.Println("   ⚠️  govulncheck not installed. Installing...")
		installCmd := exec.Command("go", "install", "golang.org/x/vuln/cmd/govulncheck@latest")
		if err := installCmd.Run(); err != nil {
			fmt.Printf("   ❌ Failed to install govulncheck: %v\n", err)
			return
		}
	}

	// Run vulnerability check
	cmd := exec.Command("govulncheck", "./...")
	output, err := cmd.Output()

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 3 {
			fmt.Println("   🔒 Vulnerabilities found:")
			fmt.Println(string(output))
		} else {
			fmt.Printf("   ❌ Error running security scan: %v\n", err)
		}
	} else {
		fmt.Println("   ✅ No known vulnerabilities found!")
	}
}

func generateRecommendations() {
	fmt.Println("\n💡 Recommendations")
	fmt.Println("------------------")

	recommendations := []string{
		"🔄 Run 'go get -u ./...' to update all dependencies to latest versions",
		"🧹 Run 'go mod tidy' to clean up unused dependencies",
		"🔒 Regularly run 'govulncheck ./...' to check for security vulnerabilities",
		"📅 Review dependencies quarterly for updates and security patches",
		"🔍 Consider using 'go mod graph' to visualize dependency relationships",
		"⚡ For production, pin specific versions to avoid unexpected updates",
		"🧪 Test thoroughly after updating dependencies",
		"📊 Monitor dependency licenses for compliance requirements",
	}

	for _, rec := range recommendations {
		fmt.Printf("   %s\n", rec)
	}

	fmt.Println("\n🚀 Automation Scripts:")
	fmt.Println("   - ./scripts/update-dependencies.sh (Linux/Mac)")
	fmt.Println("   - ./scripts/update-dependencies.ps1 (Windows)")
}

func parseDependencies(output string) []DependencyInfo {
	var dependencies []DependencyInfo

	decoder := json.NewDecoder(strings.NewReader(output))
	for decoder.More() {
		var dep DependencyInfo
		if err := decoder.Decode(&dep); err != nil {
			continue
		}
		dependencies = append(dependencies, dep)
	}

	return dependencies
}

func calculateAge(timeStr string) string {
	if timeStr == "" {
		return "unknown"
	}

	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return "unknown"
	}

	age := time.Since(t)
	days := int(age.Hours() / 24)

	switch {
	case days < 30:
		return fmt.Sprintf("%d days old", days)
	case days < 365:
		return fmt.Sprintf("%d months old", days/30)
	default:
		return fmt.Sprintf("%d years old", days/365)
	}
}
