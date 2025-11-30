package tools

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"orchestrator/pkg/demo"
)

// ComposeAddServiceTool adds a service to the compose.yml file.
type ComposeAddServiceTool struct {
	workspaceDir string
}

// NewComposeAddServiceTool creates a new compose_add_service tool.
func NewComposeAddServiceTool(workspaceDir string) *ComposeAddServiceTool {
	return &ComposeAddServiceTool{workspaceDir: workspaceDir}
}

// Name returns the tool name.
func (t *ComposeAddServiceTool) Name() string {
	return "compose_add_service"
}

// Definition returns the tool definition.
func (t *ComposeAddServiceTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_add_service",
		Description: "Add a new service to the Docker Compose file. If the compose.yml doesn't exist, creates it with the new service.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"name": {
					Type:        "string",
					Description: "The name of the service to add (e.g., 'db', 'redis', 'api')",
				},
				"image": {
					Type:        "string",
					Description: "The Docker image to use (e.g., 'postgres:16', 'redis:7-alpine')",
				},
				"ports": {
					Type:        "array",
					Description: "Port mappings (e.g., ['5432:5432', '8080:80'])",
					Items:       &Property{Type: "string"},
				},
				"environment": {
					Type:        "object",
					Description: "Environment variables as key-value pairs",
				},
				"volumes": {
					Type:        "array",
					Description: "Volume mounts (e.g., ['./data:/var/lib/postgresql/data'])",
					Items:       &Property{Type: "string"},
				},
				"depends_on": {
					Type:        "array",
					Description: "Services this service depends on",
					Items:       &Property{Type: "string"},
				},
			},
			Required: []string{"name", "image"},
		},
	}
}

// PromptDocumentation returns documentation for prompt injection.
func (t *ComposeAddServiceTool) PromptDocumentation() string {
	return `- **compose_add_service** - Add a service to Docker Compose file
  - Parameters:
    - name (required): service name (e.g., 'db', 'redis')
    - image (required): Docker image (e.g., 'postgres:16')
    - ports (optional): port mappings array
    - environment (optional): environment variables object
    - volumes (optional): volume mounts array
    - depends_on (optional): dependent services array
  - Creates compose.yml if it doesn't exist
  - Adds or updates a service with the specified configuration`
}

// Exec adds a service to the compose.yml file.
func (t *ComposeAddServiceTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return &ExecResult{Content: "Error: service name is required"}, nil
	}

	image, ok := args["image"].(string)
	if !ok || image == "" {
		return &ExecResult{Content: "Error: image is required"}, nil
	}

	// Load or create compose structure
	compose, loadErr := t.loadOrCreateCompose()
	if loadErr != nil {
		//nolint:nilerr // Errors are returned in Content for user-friendly messages
		return &ExecResult{Content: loadErr.Error()}, nil
	}

	// Build and add service
	service := buildServiceConfig(image, args)
	addServiceToCompose(compose, name, service)

	// Write back to file
	if saveErr := t.saveCompose(compose); saveErr != nil {
		//nolint:nilerr // Errors are returned in Content for user-friendly messages
		return &ExecResult{Content: saveErr.Error()}, nil
	}

	return &ExecResult{
		Content: fmt.Sprintf("Successfully added service '%s' with image '%s'", name, image),
	}, nil
}

// loadOrCreateCompose loads existing compose file or creates new structure.
func (t *ComposeAddServiceTool) loadOrCreateCompose() (map[string]any, error) {
	compose := make(map[string]any)
	composePath := demo.ComposeFilePath(t.workspaceDir)

	if !demo.ComposeFileExists(t.workspaceDir) {
		return compose, nil
	}

	content, err := os.ReadFile(composePath)
	if err != nil {
		return nil, fmt.Errorf("error reading compose file: %w", err)
	}

	if err := yaml.Unmarshal(content, &compose); err != nil {
		return nil, fmt.Errorf("error parsing compose file: %w", err)
	}

	return compose, nil
}

// buildServiceConfig builds a service configuration from args.
func buildServiceConfig(image string, args map[string]any) map[string]any {
	service := map[string]any{"image": image}

	if ports, ok := args["ports"].([]any); ok && len(ports) > 0 {
		service["ports"] = ports
	}
	if env, ok := args["environment"].(map[string]any); ok && len(env) > 0 {
		service["environment"] = env
	}
	if volumes, ok := args["volumes"].([]any); ok && len(volumes) > 0 {
		service["volumes"] = volumes
	}
	if dependsOn, ok := args["depends_on"].([]any); ok && len(dependsOn) > 0 {
		service["depends_on"] = dependsOn
	}

	return service
}

// addServiceToCompose adds a service to the compose structure.
func addServiceToCompose(compose map[string]any, name string, service map[string]any) {
	services, ok := compose["services"].(map[string]any)
	if !ok {
		services = make(map[string]any)
		compose["services"] = services
	}
	services[name] = service
}

// saveCompose writes the compose structure to file.
func (t *ComposeAddServiceTool) saveCompose(compose map[string]any) error {
	content, err := yaml.Marshal(compose)
	if err != nil {
		return fmt.Errorf("error marshaling compose file: %w", err)
	}

	composePath := demo.ComposeFilePath(t.workspaceDir)
	if err := os.WriteFile(composePath, content, 0644); err != nil {
		return fmt.Errorf("error writing compose file: %w", err)
	}

	return nil
}
