# Go Bootstrap Setup for {{.ProjectName}}

**Platform**: Go {{.GoVersion}}  
{{- if eq .ContainerImage ""}}
**Container**: 📦 **BUILD FROM DOCKERFILE REQUIRED** - Must complete first
**Dockerfile**: Will be built during bootstrap using {{.RecommendedContainer}} as base
{{- else if .NeedsContainerUpgrade}}
**Current Container**: {{.ContainerImage}}
**Recommended Container**: {{.RecommendedContainer}} ⚠️ **UPGRADE NEEDED**  
{{- else}}
**Container**: {{.ContainerImage}} ✅  
{{- end}}
**Total Issues**: {{.TotalFailures}}  
{{- if .HasCriticalFailures}}
**Priority**: 🔴 **CRITICAL** - Go infrastructure issues blocking development  
{{- else}}
**Priority**: 🟡 **HIGH** - Go infrastructure improvements needed  
{{- end}}

## Task Overview

This story addresses Go-specific infrastructure issues discovered during project initialization. The following problems must be resolved before Go development can proceed effectively:

{{- range .FailuresByPriority}}
{{- if eq .Priority 1}}
- 🔴 **CRITICAL**: {{.Description}} ({{.Component}})
{{- else if eq .Priority 2}}
- 🟡 **HIGH**: {{.Description}} ({{.Component}})
{{- else}}
- 🟢 **MEDIUM**: {{.Description}} ({{.Component}})
{{- end}}
{{- end}}

## Acceptance Criteria

{{- $defaults := .GetPlatformDefaults}}
{{- $goCommands := .GetGoSpecificCommands}}

{{- if eq .ContainerImage ""}}

### 🔴 PRIORITY 1: Container Building (DOCKERFILE MODE)
**⚠️ THIS MUST BE COMPLETED FIRST BEFORE ANY OTHER TASKS**
**🚫 BLOCKING: Container building blocks all other work since nothing else can be done without a valid container**

- [ ] Copy dockerfile from `{{.GetDockerfilePath}}` to `.maestro/Dockerfile` and build: `cp {{.GetDockerfilePath}} .maestro/Dockerfile && docker build -t maestro-{{.ProjectName}}-dev -f .maestro/Dockerfile .`
- [ ] If build fails, fix issues in `.maestro/Dockerfile` until container builds successfully
- [ ] Tag built container with name: `maestro-{{.ProjectName}}-dev`
- [ ] Validate container includes Go {{.GoVersion}}: `docker run --rm maestro-{{.ProjectName}}-dev go version`
- [ ] Test container can compile Go code: `docker run --rm -v $(pwd):/workspace -w /workspace maestro-{{.ProjectName}}-dev go build ./...`
- [ ] If necessary, extend `.maestro/Dockerfile` to include required tools: `go`, `make`, `golangci-lint`
- [ ] Use `update_container` tool to set final tag name after container build is complete
- [ ] Verify container is available to host Docker daemon (not just within bootstrap)

**CRITICAL**: Container building blocks all other work since nothing else can be done without a valid container with Go tools installed. All subsequent tasks require a working container environment to execute Go commands. Do not proceed with other sections until container building is complete.

{{- end}}

### Go Module Setup
- [ ] Initialize Go module if `go.mod` is missing: `{{index $goCommands "mod_init"}} {{.GitRepoURL}}`
- [ ] Ensure Go version {{.GoVersion}} is specified in `go.mod`
- [ ] Verify `{{index $goCommands "mod_tidy"}}` runs successfully
- [ ] All Go module dependencies properly resolved
{{- if .HasGoWorkspace}}
- [ ] Multi-module workspace detected: run `{{index $goCommands "work_sync"}}` to sync workspace
{{- end}}
{{- if .RequiresPrivateModules}}
- [ ] Configure private module access: `export GOPRIVATE={{.PrivateModulePattern}}`
- [ ] Set persistent Go environment: `go env -w GOPRIVATE={{.PrivateModulePattern}}`
{{- end}}

{{- if .NeedsContainerUpgrade}}

### Container Infrastructure Upgrade
- [ ] Update container from `{{.ContainerImage}}` to `{{.RecommendedContainer}}`
- [ ] Use `update_container` tool to update `.maestro/config.json`
- [ ] Verify new container includes Go {{.GoVersion}} and build tools
- [ ] Test container can compile Go code with `go build`
{{- end}}

{{- if .HasFailuresOfType "build_system"}}

### Build System Setup
{{- range .BuildSystemFailures}}
- [ ] Fix {{.Component}}: {{.Description}}
  {{- if .Details.target}}
  - Create `{{.Details.target}}` target in Makefile with `{{index $goCommands "mod_tidy"}}` integration
  {{- end}}
  {{- if .Details.action}}
  - Action: {{.Details.action}}
  {{- end}}
{{- end}}
- [ ] Ensure `build` target includes: `{{index $goCommands "mod_tidy"}} && go build -o bin/{{.ProjectName}} ./...`
- [ ] Verify all targets work: `{{.BuildCommand}}`, `{{.TestCommand}}`, `{{.LintCommand}}`, `{{.RunCommand}}`
{{- end}}

### Go Linting Configuration
- [ ] Install golangci-lint: `{{index $goCommands "install_golangci"}}`
- [ ] Create `.golangci.yml` with aggressive linting rules (see configuration below)
- [ ] Verify `golangci-lint run` passes
- [ ] Integrate linting into `make lint` target

### Pre-commit Hooks Setup
- [ ] Create `.git/hooks/pre-commit` requiring build, lint, and test success
- [ ] Make pre-commit hook executable: `chmod +x .git/hooks/pre-commit`
- [ ] Test pre-commit hook blocks commits on failure
- [ ] Verify hook allows commits when all checks pass

{{- if .HasFailuresOfType "container"}}
{{- if ne .ContainerImage ""}}

### Container Infrastructure - Image Upgrade Mode
{{- range .ContainerFailures}}
- [ ] Resolve container issue: {{.Description}}
  {{- if .Details.image_name}}
  - Image: {{.Details.image_name}}
  {{- end}}
  {{- if .Details.error_message}}
  - Error: {{.Details.error_message}}
  {{- end}}
{{- end}}
- [ ] Validate container can compile Go code: `go build ./...`
- [ ] Ensure rootless execution works: `--user=nobody --read-only --network=none`
- [ ] Test golangci-lint is available in container
{{- end}}
{{- end}}

{{- if .HasFailuresOfType "binary_size"}}

### Large File Management
{{- range .BinarySizeFailures}}
- [ ] Address large file: {{.Details.file_path}} ({{.Details.file_size}})
  {{- if eq .Details.action "setup_git_lfs"}}
  - Set up Git LFS for files over {{.Details.threshold}}
  - Run: `git lfs track "{{.Details.file_path}}"`
  {{- end}}
{{- end}}
- [ ] Verify no Go binaries exceed 100MB hard limit
- [ ] Configure Git LFS for large assets if needed
{{- end}}

{{- if .HasFailuresOfType "git_access"}}

### Git Repository Access
{{- range .GitAccessFailures}}
- [ ] Fix git issue: {{.Description}}
  {{- if eq .Details.action "create_branch"}}
  - Create and push target branch: `git checkout -b {{.Details.branch}} && git push -u origin {{.Details.branch}}`
  - Ensure branch has at least one commit (create initial commit if needed)
  - Verify branch is available on remote: `git ls-remote origin {{.Details.branch}}`
  {{- else if .Details.action}}
  - Action: {{.Details.action}}
  {{- end}}
{{- end}}
- [ ] Verify git mirror and worktree creation works
- [ ] Test SSH key access to repository
{{- end}}

## Implementation Plan

### Phase 1: Go Environment Setup

**1. Go Module Initialization**
```bash
# Check if go.mod exists
if [ ! -f "go.mod" ]; then
    {{index $goCommands "mod_init"}} {{.ProjectName}}
fi

# Ensure correct Go version in go.mod
go mod edit -go={{.GoVersion}}
{{index $goCommands "mod_tidy"}}
```

**2. Version Detection and Validation**
```bash
# Check current Go version
{{index $goCommands "version_check"}}

# Get latest stable Go version (requires curl and jq)
LATEST_GO=$({{index $goCommands "latest_version_cmd"}})
echo "Latest stable Go version: $LATEST_GO"

# Use Go {{.GoVersion}} (recommended for this project)
```

{{- if .NeedsContainerUpgrade}}

**3. Container Upgrade**
```bash
# Update container configuration
update_container --container-image "{{.RecommendedContainer}}"
update_container --go-version "{{.GoVersion}}"

# Verify new container works
docker pull {{.RecommendedContainer}}
docker run --rm {{.RecommendedContainer}} go version
```
{{- end}}

### Phase 2: Build System Configuration

**1. Makefile Creation/Update**
```makefile
# Makefile with Go-specific targets
.PHONY: build test lint run clean

build:
	{{index $goCommands "mod_tidy"}}
	go build -o bin/{{.ProjectName}} ./...

test:
	{{index $goCommands "mod_tidy"}}
	go test ./...

lint:
	golangci-lint run

run:
	go run ./...

clean:
	rm -rf bin/
	go clean
```

**2. Linting Setup**
```bash
# Install golangci-lint
{{index $goCommands "install_golangci"}}

# Verify installation
{{index $goCommands "golangci_version"}}

# Create .golangci.yml (see configuration below)
```

### Phase 3: Pre-commit Hooks

**1. Create Pre-commit Hook**
```bash
#!/bin/bash
# .git/hooks/pre-commit

set -e

echo "Running pre-commit checks..."

# Run build
echo "🔨 Building..."
{{.BuildCommand}}

# Run tests
echo "🧪 Testing..."
{{.TestCommand}}

# Run linting
echo "🔍 Linting..."
{{.LintCommand}}

echo "✅ All pre-commit checks passed!"
```

**2. Install Hook**
```bash
chmod +x .git/hooks/pre-commit
```

### Phase 4: Validation

**1. Full Development Workflow Test**
```bash
# Test build pipeline
{{.CleanCommand}}
{{.BuildCommand}}
{{.TestCommand}}
{{.LintCommand}}

# Test pre-commit hook
git add .
git commit -m "Test commit"  # Should pass all checks

# Test container environment
docker run --rm -v $(pwd):/workspace -w /workspace {{.RecommendedContainer}} {{.BuildCommand}}
```

## Configuration Files

### .golangci.yml
```yaml
{{.GetGoLintConfig}}
```

### Pre-commit Hook
```bash
#!/bin/bash
# .git/hooks/pre-commit

set -e
echo "🔍 Running pre-commit checks..."

echo "🔨 Building..."
if ! {{.BuildCommand}}; then
    echo "❌ Build failed"
    exit 1
fi

echo "🧪 Testing..."
if ! {{.TestCommand}}; then
    echo "❌ Tests failed"
    exit 1
fi

echo "🔍 Linting..."
if ! {{.LintCommand}}; then
    echo "❌ Linting failed"
    exit 1
fi

echo "✅ All pre-commit checks passed!"
```

## Technical Notes

### Go Version Management
- **Target Version**: Go {{.GoVersion}}
- **Detection Command**: `{{index $goCommands "version_check"}}`
- **Latest Stable**: `{{index $goCommands "latest_version_cmd"}}`
- **Module Requirement**: Will be set in `go.mod`

### Container Requirements
{{- if eq .ContainerImage ""}}
- **Mode**: Dockerfile build - container will be built from `{{.GetDockerfilePath}}`
- **Target**: `maestro-{{.ProjectName}}-dev` (final tagged image)
- **Requirements**: Must include Go {{.GoVersion}}, make, golangci-lint
{{- else if .NeedsContainerUpgrade}}
- **Current**: {{.ContainerImage}} (requires upgrade)
- **Target**: {{.RecommendedContainer}} (Go-optimized)
- **Upgrade**: Required - includes Go {{.GoVersion}} and build tools
{{- else}}
- **Container**: {{.ContainerImage}} ✅
- **Go Version**: Includes Go {{.GoVersion}}
{{- end}}
- **Security**: Rootless execution (`--user=nobody`), read-only filesystem with writable `/tmp`
- **Network**: {{if .RequiresNetworkAccess}}Required during setup, disabled after (`--network=none`){{else}}Disabled (`--network=none`){{end}}

### Build System Integration
- **Module Management**: `{{index $goCommands "mod_tidy"}}` integrated into build target
- **Binary Output**: `bin/{{.ProjectName}}`
- **Linting**: golangci-lint with aggressive configuration
- **Pre-commit**: Enforces build + test + lint before commits

### File System Constraints
- **Large File Limit**: 100MB (GitHub push limit)
- **Binary Optimization**: Consider build flags for smaller binaries
- **Git LFS**: {{if .HasFailuresOfType "binary_size"}}Required for large assets{{else}}Not currently needed{{end}}

## Success Criteria

✅ **Go module properly initialized with version {{.GoVersion}}**  
✅ **All Makefile targets functional (build, test, lint, run)**  
✅ **golangci-lint installed and configured with aggressive rules**  
✅ **Pre-commit hooks enforcing quality gates**  
✅ **Container upgraded to Go-optimized image** {{if .NeedsContainerUpgrade}}({{.RecommendedContainer}}){{end}}  
✅ **Full verification suite passes**  
✅ **Build pipeline works in container environment**  
✅ **No files exceed size limits**  

Upon completion, mark `bootstrap_complete = true` in project configuration and proceed with Go development workflow.

---

*Generated by Maestro Bootstrap System - Go-specific template*