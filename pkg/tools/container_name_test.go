package tools

import (
	"testing"
)

func TestIsReservedContainerName(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		wantReserved  bool
	}{
		{
			name:          "bootstrap with latest tag",
			containerName: "maestro-bootstrap:latest",
			wantReserved:  true,
		},
		{
			name:          "bootstrap with v1 tag",
			containerName: "maestro-bootstrap:v1",
			wantReserved:  true,
		},
		{
			name:          "bootstrap without tag",
			containerName: "maestro-bootstrap",
			wantReserved:  true,
		},
		{
			name:          "bootstrap with custom tag",
			containerName: "maestro-bootstrap:abc123",
			wantReserved:  true,
		},
		{
			name:          "project container",
			containerName: "maestro-myproject:latest",
			wantReserved:  false,
		},
		{
			name:          "project container without tag",
			containerName: "maestro-myproject",
			wantReserved:  false,
		},
		{
			name:          "project container with dev suffix",
			containerName: "maestro-myproject-dev:latest",
			wantReserved:  false,
		},
		{
			name:          "completely different name",
			containerName: "ubuntu:22.04",
			wantReserved:  false,
		},
		{
			name:          "similar but different prefix",
			containerName: "maestro-bootstrapper:latest",
			wantReserved:  false,
		},
		{
			name:          "empty string",
			containerName: "",
			wantReserved:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsReservedContainerName(tt.containerName)
			if got != tt.wantReserved {
				t.Errorf("IsReservedContainerName(%q) = %v, want %v", tt.containerName, got, tt.wantReserved)
			}
		})
	}
}

func TestReservedContainerNameError(t *testing.T) {
	err := &ReservedContainerNameError{ContainerName: "maestro-bootstrap:latest"}
	errMsg := err.Error()

	// Verify error message contains the container name
	if errMsg == "" {
		t.Error("Error message should not be empty")
	}

	// Verify error mentions the reserved name
	if !contains(errMsg, "maestro-bootstrap:latest") {
		t.Errorf("Error message should contain the container name, got: %s", errMsg)
	}

	// Verify error mentions it's reserved
	if !contains(errMsg, "reserved") {
		t.Errorf("Error message should mention 'reserved', got: %s", errMsg)
	}
}

func TestGenerateContainerName(t *testing.T) {
	tests := []struct {
		name           string
		projectName    string
		dockerfilePath string
		want           string
	}{
		{
			name:           "simple project with default dockerfile",
			projectName:    "myapp",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-myapp-dockerfile:latest",
		},
		{
			name:           "project with GPU dockerfile",
			projectName:    "myapp",
			dockerfilePath: ".maestro/Dockerfile.gpu",
			want:           "maestro-myapp-dockerfile-gpu:latest",
		},
		{
			name:           "project with dev dockerfile",
			projectName:    "myapp",
			dockerfilePath: ".maestro/Dockerfile-dev",
			want:           "maestro-myapp-dockerfile-dev:latest",
		},
		{
			name:           "project name with spaces",
			projectName:    "My App",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-my-app-dockerfile:latest",
		},
		{
			name:           "project name with underscores",
			projectName:    "my_app",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-my-app-dockerfile:latest",
		},
		{
			name:           "project name with uppercase",
			projectName:    "MyApp",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-myapp-dockerfile:latest",
		},
		{
			name:           "absolute container path",
			projectName:    "testproject",
			dockerfilePath: "/workspace/.maestro/Dockerfile.test",
			want:           "maestro-testproject-dockerfile-test:latest",
		},
		{
			name:           "empty project name",
			projectName:    "",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-project-dockerfile:latest",
		},
		{
			name:           "special characters in project name",
			projectName:    "my@app#123",
			dockerfilePath: ".maestro/Dockerfile",
			want:           "maestro-myapp123-dockerfile:latest",
		},
		{
			name:           "empty dockerfile path",
			projectName:    "myapp",
			dockerfilePath: "",
			want:           "maestro-myapp-dockerfile:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateContainerName(tt.projectName, tt.dockerfilePath)
			if got != tt.want {
				t.Errorf("GenerateContainerName(%q, %q) = %q, want %q",
					tt.projectName, tt.dockerfilePath, got, tt.want)
			}

			// Verify generated name is not reserved
			if IsReservedContainerName(got) {
				t.Errorf("GenerateContainerName returned reserved name: %s", got)
			}
		})
	}
}

func TestSanitizeForContainerName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyApp", "myapp"},
		{"my-app", "my-app"},
		{"my_app", "my-app"},
		{"my app", "my-app"},
		{"my--app", "my-app"},
		{"-myapp-", "myapp"},
		{"my@#$app", "myapp"},
		{"", ""},
		{"123app", "123app"},
		{"app123", "app123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeForContainerName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeForContainerName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractDockerfileIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{".maestro/Dockerfile", "dockerfile"},
		{".maestro/Dockerfile.gpu", "dockerfile-gpu"},
		{".maestro/Dockerfile-dev", "dockerfile-dev"},
		{"/workspace/.maestro/Dockerfile.test", "dockerfile-test"},
		{"Dockerfile", "dockerfile"},
		{"Dockerfile.prod", "dockerfile-prod"},
		{"", "dockerfile"},
		{"somefile.txt", "somefile-txt"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractDockerfileIdentifier(tt.input)
			if got != tt.want {
				t.Errorf("extractDockerfileIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
