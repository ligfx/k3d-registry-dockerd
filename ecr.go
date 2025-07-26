package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ECR repository detection
var ecrPattern = regexp.MustCompile(`^(\d+)\.dkr\.ecr\.([^.]+)\.amazonaws\.com(/.*)?$`)

// IsECRRepository checks if the given repository URL is an ECR repository
func IsECRRepository(repository string) bool {
	return ecrPattern.MatchString(repository)
}

// ExtractECRRegion extracts the AWS region from an ECR repository URL
func ExtractECRRegion(repository string) string {
	matches := ecrPattern.FindStringSubmatch(repository)
	if len(matches) >= 3 {
		return matches[2]
	}
	return ""
}

// ExtractECRAccountID extracts the AWS account ID from an ECR repository URL
func ExtractECRAccountID(repository string) string {
	matches := ecrPattern.FindStringSubmatch(repository)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// GetECRRegistryFromImageName extracts the ECR registry from a full image name
func GetECRRegistryFromImageName(imageName string) string {
	// Handle cases like:
	// - 123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo:tag
	// - 123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo
	parts := strings.SplitN(imageName, "/", 2)
	if len(parts) > 0 && IsECRRepository(parts[0]) {
		return parts[0]
	}
	return ""
}

// ECRCredentialHelper represents the response from docker-credential-ecr-login
type ECRCredentialHelper struct {
	Username string `json:"Username"`
	Secret   string `json:"Secret"`
}

// GetECRAuthToken gets an ECR auth token using the ECR credential helper
func GetECRAuthToken(ctx context.Context, registry string) (*ImageAuthConfig, error) {
	if !IsECRRepository(registry) {
		return nil, fmt.Errorf("not an ECR repository: %s", registry)
	}

	// Check if we have AWS credentials available
	if !hasAWSCredentials() {
		log.Printf("No AWS credentials detected for ECR registry %s", registry)
		return nil, nil
	}

	// Use docker-credential-ecr-login to get the auth token
	cmd := exec.CommandContext(ctx, "docker-credential-ecr-login", "get")
	cmd.Stdin = strings.NewReader(registry)

	// Pass through AWS environment variables
	cmd.Env = append(os.Environ(), getAWSEnvironmentVariables()...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get ECR credentials for %s: %w", registry, err)
	}

	var creds ECRCredentialHelper
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse ECR credentials: %w", err)
	}

	log.Printf("Successfully obtained ECR credentials for %s (username: %s)", registry, creds.Username)

	return &ImageAuthConfig{
		Username: creds.Username,
		Password: creds.Secret,
	}, nil
}

// hasAWSCredentials checks if AWS credentials are available
func hasAWSCredentials() bool {
	// Check for standard AWS environment variables
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return true
	}

	// Check for AWS profile
	if os.Getenv("AWS_PROFILE") != "" {
		return true
	}

	// Check for AWS config/credentials files
	homeDir, err := os.UserHomeDir()
	if err == nil {
		awsDir := homeDir + "/.aws"
		if _, err := os.Stat(awsDir + "/credentials"); err == nil {
			return true
		}
		if _, err := os.Stat(awsDir + "/config"); err == nil {
			return true
		}
	}

	// Check for ECS/EC2 instance metadata (container role)
	if os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
		return true
	}

	return false
}

// getAWSEnvironmentVariables returns all AWS-related environment variables
func getAWSEnvironmentVariables() []string {
	var awsVars []string

	// Standard AWS environment variables
	awsEnvVars := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_PROFILE",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_ROLE_ARN",
		"AWS_ROLE_SESSION_NAME",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CA_BUNDLE",
		"AWS_METADATA_SERVICE_TIMEOUT",
		"AWS_METADATA_SERVICE_NUM_ATTEMPTS",
		"AWS_STS_REGIONAL_ENDPOINTS",
		"AWS_SDK_LOAD_CONFIG",
	}

	for _, envVar := range awsEnvVars {
		if value := os.Getenv(envVar); value != "" {
			awsVars = append(awsVars, fmt.Sprintf("%s=%s", envVar, value))
		}
	}

	// Also pass HOME for credential file resolution
	if home := os.Getenv("HOME"); home != "" {
		awsVars = append(awsVars, fmt.Sprintf("HOME=%s", home))
	}

	return awsVars
}
