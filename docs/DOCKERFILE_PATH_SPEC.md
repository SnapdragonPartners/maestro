# Dockerfile Path Standardization Spec

## Problem Statement

The current implementation has several issues with Dockerfile handling:

1. **Conflict Risk**: Using repo root `Dockerfile` can conflict with production Dockerfiles
2. **No Default Set**: Config has a `Dockerfile` field but it's never set to a default value
3. **Hardcoded Paths**: Multiple places hardcode "Dockerfile" without using config
4. **No Auto-Recovery**: Bootstrap doesn't attempt to build from Dockerfile if container is unavailable
5. **No Path Constraints**: Agents could accidentally overwrite production Dockerfiles
6. **Inconsistent Naming**: Tools use both `dockerfile` and `dockerfile_path` parameters

## Design Goals

1. **Isolation**: Maestro development Dockerfiles live in `.maestro/` to avoid conflicts
2. **Flexibility**: Allow multiple Dockerfile variants (e.g., `.maestro/Dockerfile.dev`, `.maestro/Dockerfile.cuda`)
3. **Consistency**: Single source of truth for Dockerfile path via config; standardized parameter naming
4. **Safety**: Constrain tools to only use Dockerfiles in `.maestro/` directory with proper path validation
5. **Robustness**: Warn when Dockerfile missing even if container is valid (future recovery capability)

## Terminology

- **Bootstrap mode**: Operating from safe/fallback container; should attempt to restore proper dev container
- **Pinned image**: The project-specific development container image (stored as `PinnedImageID`)
- **Safe image**: The guaranteed baseline fallback container (stored as `SafeImageID`)
- **Dockerfile parameter**: Standardized name is `dockerfile` (not `dockerfile_path`) to match config field

---

# Phase 1: Path Standardization

## 1. Config Changes (`pkg/config/config.go`)

### New Constants and Functions

```go
const (
    // DefaultDockerfilePath is the standard location for Maestro development Dockerfiles.
    // Using .maestro/ directory avoids conflicts with production Dockerfiles in repo root.
    DefaultDockerfilePath = ".maestro/Dockerfile"

    // MaestroDockerfileDir is the directory where all Maestro Dockerfiles must reside.
    MaestroDockerfileDir = ".maestro"
)

// GetDockerfilePath returns the configured Dockerfile path, or the default if not set.
// The path is always relative to the project/workspace root.
func GetDockerfilePath() string {
    mu.RLock()
    defer mu.RUnlock()

    if config != nil && config.Container != nil && config.Container.Dockerfile != "" {
        return config.Container.Dockerfile
    }
    return DefaultDockerfilePath
}

// SetDockerfilePath updates the Dockerfile path in config.
// Returns error if path is not within the .maestro directory.
func SetDockerfilePath(dockerfilePath string) error {
    if !IsValidDockerfilePath(dockerfilePath) {
        return fmt.Errorf("dockerfile must be in %s/ directory, got: %s", MaestroDockerfileDir, dockerfilePath)
    }

    mu.Lock()
    defer mu.Unlock()

    if config.Container == nil {
        config.Container = &ContainerConfig{}
    }
    config.Container.Dockerfile = dockerfilePath
    return saveConfigLocked()
}

// IsValidDockerfilePath checks if a Dockerfile path is within the allowed .maestro directory.
// Uses proper path resolution to prevent escapes via ".." segments.
func IsValidDockerfilePath(dockerfilePath string) bool {
    if dockerfilePath == "" {
        return false
    }

    // Clean the path to resolve any ".." segments
    cleanPath := filepath.Clean(dockerfilePath)

    // Reject absolute paths
    if filepath.IsAbs(cleanPath) {
        return false
    }

    // Must start with .maestro/
    if !strings.HasPrefix(cleanPath, MaestroDockerfileDir+string(filepath.Separator)) {
        return false
    }

    // Double-check by computing relative path from .maestro
    // This catches edge cases like ".maestro/../etc/passwd"
    rel, err := filepath.Rel(MaestroDockerfileDir, cleanPath)
    if err != nil {
        return false
    }

    // If relative path starts with "..", it escaped the directory
    if strings.HasPrefix(rel, "..") {
        return false
    }

    return true
}

// IsValidDockerfilePathWithRoot validates a Dockerfile path with explicit project root.
// Used when validating paths provided as tool arguments.
func IsValidDockerfilePathWithRoot(projectRoot, dockerfilePath string) bool {
    if dockerfilePath == "" {
        return false
    }

    // Resolve to absolute paths
    absProject, err := filepath.Abs(projectRoot)
    if err != nil {
        return false
    }

    // Handle both relative and absolute dockerfile paths
    var absDockerfile string
    if filepath.IsAbs(dockerfilePath) {
        absDockerfile = filepath.Clean(dockerfilePath)
    } else {
        absDockerfile = filepath.Clean(filepath.Join(absProject, dockerfilePath))
    }

    // The dockerfile must be within <projectRoot>/.maestro/
    maestroDir := filepath.Join(absProject, MaestroDockerfileDir)

    rel, err := filepath.Rel(maestroDir, absDockerfile)
    if err != nil {
        return false
    }

    // If relative path starts with "..", it's outside .maestro
    return !strings.HasPrefix(rel, "..")
}
```

### Config Initialization

Update `applyContainerDefaults()` to set default Dockerfile path:

```go
func applyContainerDefaults() {
    if config.Container == nil {
        config.Container = &ContainerConfig{}
    }
    if config.Container.Dockerfile == "" {
        config.Container.Dockerfile = DefaultDockerfilePath
    }
    // ... existing defaults ...
}
```

## 2. Container Tools Updates

### Parameter Naming Standardization

All tools use `dockerfile` parameter (not `dockerfile_path`) to match config field name.

### `pkg/tools/container_common.go`

Remove the old constant:

```go
// REMOVE:
// const DefaultDockerfile = "Dockerfile"

// Tools should use config.GetDockerfilePath() instead
```

### `pkg/tools/container_build.go`

Update parameter name and validation:

```go
func (c *ContainerBuildTool) Definition() ToolDefinition {
    return ToolDefinition{
        // ...
        Properties: map[string]Property{
            // ...
            "dockerfile": {  // Changed from "dockerfile_path"
                Type:        "string",
                Description: "Path to Dockerfile within .maestro/ directory (defaults to .maestro/Dockerfile)",
            },
            // ...
        },
    }
}

func (c *ContainerBuildTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
    cwd := extractWorkingDirectory(args)
    cwd = c.translateToHostPath(cwd)

    // Extract dockerfile path - use config default if not provided
    dockerfilePath := config.GetDockerfilePath()
    if path, ok := args["dockerfile"].(string); ok && path != "" {
        // Validate path is within .maestro directory
        if !config.IsValidDockerfilePathWithRoot(cwd, path) {
            return nil, fmt.Errorf("dockerfile must be within .maestro/ directory (got: %s). "+
                "This prevents accidentally modifying production Dockerfiles", path)
        }
        dockerfilePath = path
    }

    // ... rest of build logic using dockerfilePath ...
}

func (c *ContainerBuildTool) PromptDocumentation() string {
    return `- **container_build** - Build Docker container from Dockerfile using buildx
  - Parameters:
    - container_name (required): name to tag the built container
    - cwd (optional): working directory (project root)
    - dockerfile (optional): path within .maestro/ directory (defaults to .maestro/Dockerfile)
    - platform (optional): target platform for multi-arch builds
  - IMPORTANT: Dockerfile must be in .maestro/ directory to avoid conflicts with production Dockerfiles
  - If adapting an existing repo Dockerfile, copy it to .maestro/ first`
}
```

### `pkg/tools/container_update.go`

Update to persist Dockerfile path to config:

```go
func (c *ContainerUpdateTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
    // ... existing validation ...

    // Extract and validate dockerfile path
    if path, ok := args["dockerfile"].(string); ok && path != "" {
        if !config.IsValidDockerfilePath(path) {
            return nil, fmt.Errorf("dockerfile must be within .maestro/ directory (got: %s)", path)
        }
        if err := config.SetDockerfilePath(path); err != nil {
            return nil, fmt.Errorf("failed to update dockerfile path: %w", err)
        }
    }

    // ... rest of update logic ...
}
```

## 3. Bootstrap Detection Enhancement (`pkg/pm/bootstrap.go`)

Update `detectMissingDockerfile` with warn-only behavior:

```go
// detectMissingDockerfile checks if development container is properly configured.
// Returns false (no bootstrap needed) if:
//   1. Container is configured with valid pinned image (warns if Dockerfile missing), OR
//   2. Container invalid but .maestro/Dockerfile exists (can be built)
// Returns true (bootstrap needed) if no valid container AND no Dockerfile.
func (bd *BootstrapDetector) detectMissingDockerfile() bool {
    cfg, err := config.GetConfig()
    if err != nil {
        bd.logger.Debug("Failed to get config: %v", err)
        return true
    }

    dockerfilePath := config.GetDockerfilePath()
    fullPath := filepath.Join(bd.projectDir, dockerfilePath)
    dockerfileExists := bd.fileExists(fullPath)

    // Check 1: Is there already a working container configured?
    if bd.hasValidContainer(cfg) {
        // Container is valid - warn if Dockerfile missing but don't require bootstrap
        if !dockerfileExists {
            bd.logger.Warn("Development container is valid but %s not found. "+
                "Future container rebuilds may fail.", dockerfilePath)
        }
        return false // No bootstrap needed
    }

    // Container is not valid - check if we can build from Dockerfile
    if dockerfileExists {
        bd.logger.Debug("Found Maestro Dockerfile at %s - can rebuild container", fullPath)
        return false // Dockerfile exists, can rebuild
    }

    bd.logger.Debug("No valid container and no Dockerfile at %s", fullPath)
    return true // Bootstrap needed - must create Dockerfile
}

// hasValidContainer checks if a working container is already configured.
func (bd *BootstrapDetector) hasValidContainer(cfg *config.Config) bool {
    if cfg.Container == nil || cfg.Container.Name == "" {
        return false
    }

    if cfg.Container.Name == config.BootstrapContainerTag {
        return false // Still using bootstrap fallback
    }

    if cfg.Container.PinnedImageID == "" {
        return false // Not built/configured
    }

    return bd.validateDockerImage(cfg.Container.PinnedImageID)
}

// fileExists checks if a file exists and is readable.
func (bd *BootstrapDetector) fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}
```

## 4. Coder Testing Updates (`pkg/coder/testing.go`)

Update hardcoded paths to use config:

```go
func (c *Coder) handleDevOpsStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, workspacePathStr string) (proto.State, bool, error) {
    c.logger.Info("DevOps story testing: focusing on infrastructure validation")

    // Check for Dockerfile in the configured location (within .maestro/)
    dockerfilePath := filepath.Join(workspacePathStr, config.GetDockerfilePath())
    if fileExists(dockerfilePath) {
        return c.handleContainerTesting(ctx, sm, workspacePathStr, dockerfilePath)
    }

    // ... rest of function ...
}
```

## 5. Bootstrap Template Updates (`pkg/templates/bootstrap/bootstrap.tpl.md`)

Update the container/Dockerfile section in the template.

---

# Phase 2: Container Recovery Ladder (Future)

## Overview

Phase 2 implements a robust container recovery system that automatically handles container/image failures.

## Recovery Sequence

When Maestro needs a working container:

1. **Recreate from pinned image**: If container instance missing but `PinnedImageID` exists, recreate container
2. **Rebuild from Dockerfile**: If pinned image missing/unavailable AND `.maestro/Dockerfile` exists, rebuild and update pinned image
3. **Fallback to safe image**: If no Dockerfile or rebuild fails, use `SafeImageID`
4. **Rebuild safe image**: If safe image missing, rebuild from embedded bootstrap Dockerfile
5. **Fatal error**: If safe image rebuild fails, app cannot proceed

## Bootability Validation

- Image existence check is not sufficient
- Must verify container can actually start (bootability check)
- Use existing `ValidateContainerCapabilities()` from `pkg/tools/container_common.go`
- If image exists but not bootable → treat as missing, proceed to next recovery step

## Bootstrap Mode Definition

Bootstrap mode means:
- Operating from safe/fallback container
- Should attempt to restore proper dev container when possible
- Triggered when recovery falls through to safe image (steps 3-4)
- NOT triggered merely by missing Dockerfile when container is valid

## Files to Modify (Phase 2)

| File | Changes |
|------|---------|
| `pkg/pm/bootstrap.go` | Add recovery ladder logic |
| `pkg/coder/driver.go` | Integrate recovery on container failure |
| `internal/orch/startup.go` | Pre-flight recovery checks |
| `pkg/tools/container_common.go` | Add bootability check helper |

---

# Implementation Plan

## Phase 1 Files to Modify

| File | Changes |
|------|---------|
| `pkg/config/config.go` | Add constants, `GetDockerfilePath()`, `SetDockerfilePath()`, `IsValidDockerfilePath()`, `IsValidDockerfilePathWithRoot()` |
| `pkg/config/config_test.go` | Add tests for new functions |
| `pkg/tools/container_common.go` | Remove `DefaultDockerfile` constant |
| `pkg/tools/container_build.go` | Use config getter, rename param to `dockerfile`, add validation |
| `pkg/tools/container_update.go` | Persist dockerfile path to config |
| `pkg/pm/bootstrap.go` | Update `detectMissingDockerfile()` with warn-only behavior |
| `pkg/coder/testing.go` | Use config getter for path |
| `pkg/templates/bootstrap/bootstrap.tpl.md` | Update container section |

## Phase 1 Test Plan

### Unit Tests

| Test | File | Description |
|------|------|-------------|
| `TestGetDockerfilePath_Default` | `config_test.go` | Returns `.maestro/Dockerfile` when not set |
| `TestGetDockerfilePath_Configured` | `config_test.go` | Returns configured value |
| `TestSetDockerfilePath_Valid` | `config_test.go` | Accepts paths in `.maestro/` |
| `TestSetDockerfilePath_Invalid` | `config_test.go` | Rejects paths outside `.maestro/` |
| `TestIsValidDockerfilePath_DotDot` | `config_test.go` | Rejects `..` escape attempts |
| `TestIsValidDockerfilePath_Absolute` | `config_test.go` | Rejects absolute paths |
| `TestContainerBuild_PathConstraint` | `container_build_test.go` | Rejects non-`.maestro` paths |
| `TestDetectMissingDockerfile_ValidContainerNoFile` | `bootstrap_test.go` | Warns but returns false |

## Phase 1 Acceptance Criteria

- [ ] `config.GetDockerfilePath()` returns `.maestro/Dockerfile` by default
- [ ] `config.SetDockerfilePath()` validates path is in `.maestro/`
- [ ] `config.IsValidDockerfilePath()` catches `..` escape attempts
- [ ] `container_build` uses `dockerfile` param (not `dockerfile_path`)
- [ ] `container_build` rejects dockerfile outside `.maestro/`
- [ ] `container_update` persists dockerfile path to config
- [ ] Bootstrap detection warns if Dockerfile missing but container valid
- [ ] Bootstrap detection returns false if `.maestro/Dockerfile` exists
- [ ] Coder testing uses config path, not hardcoded
- [ ] All existing tests pass
- [ ] New validation tests added

---

*Created: 2025-01-12*
*Updated: 2025-01-12 (incorporated review feedback)*
*Branch: rc2*
