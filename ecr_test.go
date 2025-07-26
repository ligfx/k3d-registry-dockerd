package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestIsECRRepository(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		expected   bool
	}{
		{
			name:       "valid ECR repository",
			repository: "123456789012.dkr.ecr.us-west-2.amazonaws.com",
			expected:   true,
		},
		{
			name:       "ECR repository with path",
			repository: "123456789012.dkr.ecr.eu-central-1.amazonaws.com/my-repo",
			expected:   true,
		},
		{
			name:       "docker.io repository",
			repository: "docker.io/library/nginx",
			expected:   false,
		},
		{
			name:       "gcr.io repository",
			repository: "gcr.io/my-project/my-image",
			expected:   false,
		},
		{
			name:       "localhost registry",
			repository: "localhost:5000",
			expected:   false,
		},
		{
			name:       "invalid ECR format",
			repository: "invalid.dkr.ecr.us-west-2.amazonaws.com",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsECRRepository(tt.repository)
			if result != tt.expected {
				t.Errorf("IsECRRepository(%q) = %v, want %v", tt.repository, result, tt.expected)
			}
		})
	}
}

func TestExtractECRRegion(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		expected   string
	}{
		{
			name:       "us-west-2 region",
			repository: "123456789012.dkr.ecr.us-west-2.amazonaws.com",
			expected:   "us-west-2",
		},
		{
			name:       "eu-central-1 region",
			repository: "123456789012.dkr.ecr.eu-central-1.amazonaws.com",
			expected:   "eu-central-1",
		},
		{
			name:       "non-ECR repository",
			repository: "docker.io/library/nginx",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractECRRegion(tt.repository)
			if result != tt.expected {
				t.Errorf("ExtractECRRegion(%q) = %q, want %q", tt.repository, result, tt.expected)
			}
		})
	}
}

func TestExtractECRAccountID(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		expected   string
	}{
		{
			name:       "valid account ID",
			repository: "123456789012.dkr.ecr.us-west-2.amazonaws.com",
			expected:   "123456789012",
		},
		{
			name:       "different account ID",
			repository: "987654321098.dkr.ecr.eu-central-1.amazonaws.com",
			expected:   "987654321098",
		},
		{
			name:       "non-ECR repository",
			repository: "docker.io/library/nginx",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractECRAccountID(tt.repository)
			if result != tt.expected {
				t.Errorf("ExtractECRAccountID(%q) = %q, want %q", tt.repository, result, tt.expected)
			}
		})
	}
}

func TestGetECRRegistryFromImageName(t *testing.T) {
	tests := []struct {
		name      string
		imageName string
		expected  string
	}{
		{
			name:      "ECR image with tag",
			imageName: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo:latest",
			expected:  "123456789012.dkr.ecr.us-west-2.amazonaws.com",
		},
		{
			name:      "ECR image without tag",
			imageName: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo",
			expected:  "123456789012.dkr.ecr.us-west-2.amazonaws.com",
		},
		{
			name:      "Docker Hub image",
			imageName: "nginx:latest",
			expected:  "",
		},
		{
			name:      "GCR image",
			imageName: "gcr.io/my-project/my-image:latest",
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetECRRegistryFromImageName(tt.imageName)
			if result != tt.expected {
				t.Errorf("GetECRRegistryFromImageName(%q) = %q, want %q", tt.imageName, result, tt.expected)
			}
		})
	}
}

func TestHasAWSCredentials(t *testing.T) {
	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	tests := []struct {
		name     string
		envVars  map[string]string
		expected bool
	}{
		{
			name: "with access key and secret",
			envVars: map[string]string{
				"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
				"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
			expected: true,
		},
		{
			name: "with AWS profile",
			envVars: map[string]string{
				"AWS_PROFILE": "test-profile",
			},
			expected: true,
		},
		{
			name: "with container credentials",
			envVars: map[string]string{
				"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/v2/credentials/uuid",
			},
			expected: true,
		},
		{
			name:     "no credentials",
			envVars:  map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			os.Clearenv()

			// Set test environment variables
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			result := hasAWSCredentials()
			if result != tt.expected {
				t.Errorf("hasAWSCredentials() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetECRAuthToken_NoCredentials(t *testing.T) {
	// Save original environment
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	// Clear environment to simulate no AWS credentials
	os.Clearenv()

	ctx := context.Background()
	registry := "123456789012.dkr.ecr.us-west-2.amazonaws.com"

	auth, err := GetECRAuthToken(ctx, registry)

	if err != nil {
		t.Errorf("GetECRAuthToken() with no credentials should not error, got: %v", err)
	}

	if auth != nil {
		t.Errorf("GetECRAuthToken() with no credentials should return nil auth, got: %v", auth)
	}
}

func TestGetECRAuthToken_InvalidRegistry(t *testing.T) {
	ctx := context.Background()
	registry := "docker.io"

	auth, err := GetECRAuthToken(ctx, registry)

	if err == nil {
		t.Error("GetECRAuthToken() with invalid registry should return error")
	}

	if auth != nil {
		t.Errorf("GetECRAuthToken() with invalid registry should return nil auth, got: %v", auth)
	}
}
