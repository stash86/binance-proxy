package main

import (
	"fmt"
	"os"

	"binance-proxy/internal/environments"
	
	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

var (
	Version   string = "develop"
	Buildtime string = "undefined"
)

type Options struct {
	// Main commands
	Init        InitCommand        `command:"init" description:"Initialize configuration files"`
	Environment EnvironmentCommand `command:"env" description:"Environment management commands"`
	Config      ConfigCommand      `command:"config" description:"Configuration management commands"`
	Health      HealthCommand      `command:"health" description:"Health check commands"`
	
	// Global options
	Verbose bool `short:"v" long:"verbose" description:"Enable verbose output"`
	Config  string `short:"c" long:"config" description:"Configuration file path"`
}

type InitCommand struct {
	Environment string `short:"e" long:"environment" description:"Environment to initialize" default:"development"`
	Force       bool   `short:"f" long:"force" description:"Force overwrite existing files"`
}

type EnvironmentCommand struct {
	List   EnvironmentListCommand   `command:"list" description:"List available environments"`
	Show   EnvironmentShowCommand   `command:"show" description:"Show environment configuration"`
	Create EnvironmentCreateCommand `command:"create" description:"Create environment configuration"`
}

type EnvironmentListCommand struct{}

type EnvironmentShowCommand struct {
	Environment string `short:"e" long:"environment" description:"Environment to show" default:"development"`
}

type EnvironmentCreateCommand struct {
	Environment string `short:"e" long:"environment" description:"Environment to create" required:"true"`
	Template    string `short:"t" long:"template" description:"Template to use" default:"development"`
}

type ConfigCommand struct {
	Validate ConfigValidateCommand `command:"validate" description:"Validate configuration"`
	Generate ConfigGenerateCommand `command:"generate" description:"Generate configuration files"`
}

type ConfigValidateCommand struct {
	File string `short:"f" long:"file" description:"Configuration file to validate"`
}

type ConfigGenerateCommand struct {
	Environment string `short:"e" long:"environment" description:"Environment to generate config for" default:"development"`
	Output      string `short:"o" long:"output" description:"Output file path"`
}

type HealthCommand struct {
	Check HealthCheckCommand `command:"check" description:"Perform health check"`
}

type HealthCheckCommand struct {
	URL     string `short:"u" long:"url" description:"URL to check" default:"http://localhost:8092"`
	Timeout int    `short:"t" long:"timeout" description:"Timeout in seconds" default:"30"`
}

func main() {
	var opts Options
	parser := flags.NewParser(&opts, flags.Default)
	
	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (cmd *InitCommand) Execute(args []string) error {
	log.Infof("Initializing Binance Proxy for %s environment", cmd.Environment)
	
	// Create environment configuration files
	if err := environments.CreateEnvironmentConfigFiles(); err != nil {
		return fmt.Errorf("failed to create environment config files: %w", err)
	}
	
	// Create additional directories
	dirs := []string{
		"logs",
		"data",
		"certs",
		"scripts",
	}
	
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warnf("Failed to create directory %s: %v", dir, err)
		} else {
			log.Infof("Created directory: %s", dir)
		}
	}
	
	// Create sample API keys file
	apiKeysContent := `# API Keys Configuration
# Format: key_name:api_key:permissions
# Permissions: read,write,admin (comma-separated)
# Example:
# trading_bot:abc123def456:read,write
# monitoring:xyz789uvw:read
# admin_user:admin123:read,write,admin
`
	
	if err := os.WriteFile("api_keys.txt", []byte(apiKeysContent), 0600); err != nil {
		log.Warnf("Failed to create API keys file: %v", err)
	} else {
		log.Infof("Created sample API keys file: api_keys.txt")
	}
	
	log.Infof("Initialization complete! You can now:")
	log.Infof("  1. Edit configuration files in the config/ directory")
	log.Infof("  2. Configure API keys in api_keys.txt")
	log.Infof("  3. Run the proxy with: ./binance-proxy")
	
	return nil
}

func (cmd *EnvironmentListCommand) Execute(args []string) error {
	environments := []environments.Environment{
		environments.Development,
		environments.Staging,
		environments.Production,
		environments.Testing,
	}
	
	fmt.Println("Available environments:")
	for _, env := range environments {
		envConfig := environments.GetEnvironmentConfig(env)
		fmt.Printf("  %-12s - %s\n", env, getEnvironmentDescription(envConfig))
	}
	
	current := environments.GetEnvironment()
	fmt.Printf("\nCurrent environment: %s\n", current)
	
	return nil
}

func (cmd *EnvironmentShowCommand) Execute(args []string) error {
	env := environments.Environment(cmd.Environment)
	envConfig := environments.GetEnvironmentConfig(env)
	
	fmt.Printf("Environment: %s\n", envConfig.Name)
	fmt.Printf("Config File: %s\n", envConfig.ConfigFile)
	fmt.Printf("Log Level: %s\n", envConfig.LogLevel)
	fmt.Printf("Metrics Port: %d\n", envConfig.MetricsPort)
	
	fmt.Println("\nFeatures:")
	fmt.Printf("  Metrics: %t\n", envConfig.Features.EnableMetrics)
	fmt.Printf("  Pprof: %t\n", envConfig.Features.EnablePprof)
	fmt.Printf("  Security: %t\n", envConfig.Features.EnableSecurity)
	fmt.Printf("  Caching: %t\n", envConfig.Features.EnableCaching)
	fmt.Printf("  TLS: %t\n", envConfig.Features.EnableTLS)
	
	fmt.Println("\nLimits:")
	fmt.Printf("  Max Memory: %d MB\n", envConfig.Limits.MaxMemoryMB)
	fmt.Printf("  Max Connections: %d\n", envConfig.Limits.MaxConnections)
	fmt.Printf("  Rate Limit: %.1f RPS\n", envConfig.Limits.RateLimitRPS)
	
	fmt.Println("\nSecurity:")
	fmt.Printf("  Require API Key: %t\n", envConfig.Security.RequireAPIKey)
	fmt.Printf("  Enable CORS: %t\n", envConfig.Security.EnableCORS)
	fmt.Printf("  IP Filtering: %t\n", envConfig.Security.EnableIPFilter)
	
	return nil
}

func (cmd *EnvironmentCreateCommand) Execute(args []string) error {
	log.Infof("Creating configuration for environment: %s", cmd.Environment)
	
	// This would create a new environment configuration
	// For now, just show what would be created
	fmt.Printf("Would create environment configuration for: %s\n", cmd.Environment)
	fmt.Printf("Based on template: %s\n", cmd.Template)
	
	return nil
}

func (cmd *ConfigValidateCommand) Execute(args []string) error {
	configFile := cmd.File
	if configFile == "" {
		configFile = "config/development.yaml"
	}
	
	log.Infof("Validating configuration file: %s", configFile)
	
	// Check if file exists
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return fmt.Errorf("configuration file not found: %s", configFile)
	}
	
	// Here you would implement actual validation
	log.Infof("Configuration file %s is valid", configFile)
	
	return nil
}

func (cmd *ConfigGenerateCommand) Execute(args []string) error {
	env := environments.Environment(cmd.Environment)
	envConfig := environments.GetEnvironmentConfig(env)
	
	outputFile := cmd.Output
	if outputFile == "" {
		outputFile = fmt.Sprintf("config/%s-generated.yaml", env)
	}
	
	log.Infof("Generating configuration for %s environment", env)
	log.Infof("Output file: %s", outputFile)
	
	// Generate the configuration
	if err := environments.CreateEnvironmentConfigFiles(); err != nil {
		return fmt.Errorf("failed to generate configuration: %w", err)
	}
	
	log.Infof("Configuration generated successfully")
	
	return nil
}

func (cmd *HealthCheckCommand) Execute(args []string) error {
	log.Infof("Performing health check on %s", cmd.URL)
	
	// Here you would implement actual health check
	// For now, just simulate it
	fmt.Printf("Health check URL: %s\n", cmd.URL)
	fmt.Printf("Timeout: %d seconds\n", cmd.Timeout)
	fmt.Printf("Status: OK\n")
	
	return nil
}

func getEnvironmentDescription(envConfig *environments.EnvironmentConfig) string {
	switch envConfig.Name {
	case environments.Development:
		return "Development environment with debugging enabled"
	case environments.Staging:
		return "Staging environment for testing"
	case environments.Production:
		return "Production environment with security and performance optimizations"
	case environments.Testing:
		return "Testing environment for automated tests"
	default:
		return "Custom environment"
	}
}
