# Bootstrap Project Setup for {{.ProjectName}}

**Platform**: {{.PlatformDisplayName}} ({{.Platform}})  
**Container**: {{.ContainerImage}}  
{{- if .DockerfilePath}}
**Dockerfile**: {{.DockerfilePath}}  
{{- end}}
{{- if .GitRepoURL}}
**Repository**: {{.GitRepoURL}}  
{{- end}}
**Total Issues**: {{.TotalFailures}}  
{{- if .HasCriticalFailures}}
**Priority**: 🔴 **CRITICAL** - Infrastructure issues blocking development  
{{- else}}
**Priority**: 🟡 **HIGH** - Infrastructure improvements needed  
{{- end}}

## Task Overview

This story addresses infrastructure issues discovered during project initialization. The following problems must be resolved before development can proceed effectively:

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
{{- if .HasFailuresOfType "build_system"}}

### Build System Setup
{{- range .BuildSystemFailures}}
- [ ] Fix {{.Component}}: {{.Description}}
  {{- if .Details.target}}
  - Create `{{.Details.target}}` target in Makefile
  {{- end}}
  {{- if .Details.action}}
  - Action: {{.Details.action}}
  {{- end}}
{{- end}}
- [ ] Verify all required build targets work: `{{.BuildCommand}}`, `{{.TestCommand}}`, `{{.LintCommand}}`, `{{.RunCommand}}`
{{- end}}

{{- if .HasFailuresOfType "container"}}

### Container Infrastructure
{{- range .ContainerFailures}}
- [ ] Resolve container issue: {{.Description}}
  {{- if .Details.image_name}}
  - Image: {{.Details.image_name}}
  {{- end}}
  {{- if .Details.dockerfile_path}}
  - Dockerfile: {{.Details.dockerfile_path}}
  {{- end}}
  {{- if .Details.error_message}}
  - Error: {{.Details.error_message}}
  {{- end}}
{{- end}}
- [ ] Validate container can run build and test commands
- [ ] Ensure rootless execution works with `--user=1000:1000 --read-only --network=none`
- [ ] Verify coder user (UID 1000) exists in container (required for Claude Code mode)
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
- [ ] Verify no files exceed 100MB hard limit
- [ ] Configure Git LFS if needed for 50MB+ files
{{- end}}

{{- if .HasFailuresOfType "git_access"}}

### Git Repository Access
{{- range .GitAccessFailures}}
- [ ] Fix git issue: {{.Description}}
  {{- if .Details.action}}
  - Action: {{.Details.action}}
  {{- end}}
{{- end}}
- [ ] Verify git mirror and worktree creation works
- [ ] Test SSH key access to repository
{{- end}}

{{- if .HasFailuresOfType "infrastructure"}}

### Maestro Infrastructure
{{- range .InfrastructureFailures}}
- [ ] Fix infrastructure: {{.Description}}
{{- end}}
- [ ] Verify .maestro directory structure
- [ ] Validate config.json schema
- [ ] Check database connectivity
{{- end}}

{{- if .HasFailuresOfType "external_tools"}}

### External Dependencies
{{- range .ExternalToolFailures}}
- [ ] Install missing tool: {{.Description}}
{{- end}}
- [ ] Verify all required tools are in PATH
- [ ] Check GITHUB_TOKEN environment variable
{{- end}}

## Implementation Plan

### Phase 1: Critical Infrastructure (Priority 1)
{{- $hasCritical := false}}
{{- range .FailuresByPriority}}
{{- if eq .Priority 1}}
{{- $hasCritical = true}}
{{- if eq .Type "build_system"}}

**Build System Repair**:
1. Analyze existing build configuration
2. Create/fix Makefile with standard targets
3. Test each target in container environment
4. Ensure platform-specific build commands work

{{- else if eq .Type "container"}}

**Container Setup**:
1. Diagnose container validation failure
2. {{if $.RequiresNetworkAccess}}Enable network access temporarily for setup{{else}}Fix container configuration{{end}}
3. Test image pull and basic container execution
4. Validate security constraints (rootless, read-only)

{{- else if eq .Type "binary_size"}}

**Large File Resolution** (blocking deployment):
1. Identify files over 100MB limit: {{range .BinarySizeFailures}}`{{.Details.file_path}}`{{end}}
2. Set up Git LFS or remove unnecessary large files
3. Add .gitattributes rules for future prevention

{{- end}}
{{- end}}
{{- end}}
{{- if not $hasCritical}}
*No critical infrastructure issues found.*
{{- end}}

### Phase 2: Development Quality Setup
**Essential Quality Tools** (install if not already configured):
1. **Linting Configuration**: Set up aggressive linting for code quality
   {{- if eq .Platform "go"}}
   - Install golangci-lint: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
   - Create `.golangci.yml` with aggressive rules (see Go template for config)
   {{- else if eq .Platform "node"}}
   - Install ESLint: `npm install --save-dev eslint @typescript-eslint/parser @typescript-eslint/eslint-plugin`
   - Create `.eslintrc.js` with strict TypeScript/JavaScript rules
   {{- else if eq .Platform "python"}}
   - Install Ruff: `pip install ruff`
   - Create `pyproject.toml` or `ruff.toml` with aggressive linting rules
   {{- else}}
   - Configure platform-specific linting tools for {{.Platform}}
   {{- end}}
   - Integrate linting into `{{.LintCommand}}` target
   - Ensure linting passes before proceeding

2. **Pre-commit Hooks**: Enforce quality gates automatically
   - Create `.git/hooks/pre-commit` requiring build, test, and lint success
   - Make hook executable: `chmod +x .git/hooks/pre-commit`
   - Test hook blocks commits on failure

3. **CI Configuration**: Prepare for continuous integration
   {{- if eq .Platform "go"}}
   - Consider `.github/workflows/go.yml` for GitHub Actions
   {{- else if eq .Platform "node"}}
   - Consider `.github/workflows/node.yml` for GitHub Actions
   {{- else if eq .Platform "python"}}
   - Consider `.github/workflows/python.yml` for GitHub Actions
   {{- else}}
   - Configure CI pipeline appropriate for {{.Platform}}
   {{- end}}

### Phase 3: High Priority Infrastructure Fixes (Priority 2+)
{{- range .FailuresByPriority}}
{{- if gt .Priority 1}}
1. **{{.Component}}**: {{.Description}}
   - Action: {{.Details.action}}
   {{- if .Details.file_path}}
   - File: {{.Details.file_path}}
   {{- end}}
{{- end}}
{{- end}}

### Phase 4: Validation
1. Run full verification suite: `maestro init --verify`
2. Test complete development workflow:
   - `{{.BuildCommand}}` - should compile successfully
   - `{{.TestCommand}}` - should run tests
   - `{{.LintCommand}}` - should pass linting
   - `{{.RunCommand}}` - should start application
3. Verify container security constraints
4. Test git operations (clone, worktree, PR creation)

## Technical Notes

### Platform: {{.PlatformDisplayName}}
- **Build Command**: {{.BuildCommand}}
- **Test Command**: {{.TestCommand}}
- **Lint Command**: {{.LintCommand}}
- **Run Command**: {{.RunCommand}}
- **Container Image**: {{.ContainerImage}}
{{- if .GitUserName}}
- **Git User**: {{.GitUserName}} <{{.GitUserEmail}}>
{{- end}}

### Container Configuration
{{- if .RequiresNetworkAccess}}
- **Network Access**: Required during setup phase for image pull/build
- **Security**: Will be disabled after setup (`--network=none`)
{{- else}}
- **Network Access**: Disabled (`--network=none`)
{{- end}}
- **User**: Non-root (`--user=1000:1000` - coder user required for Claude Code mode)
- **Filesystem**: Read-only with writable `/tmp`

### Required Container User (Claude Code Mode)
When using Claude Code mode, containers must have a non-root user with UID 1000:
```dockerfile
# Add to your Dockerfile - required for Claude Code mode
RUN adduser -D -u 1000 coder && \
    chown -R coder:coder /workspace
```
Claude Code refuses `--dangerously-skip-permissions` when running as root for security reasons. Maestro will attempt to create this user at runtime if missing, but pre-creating it in the Dockerfile is recommended.

### File System Constraints
- **Large File Limit**: 100MB (GitHub push limit)
- **Warning Threshold**: 50MB (recommend Git LFS)
- **Git LFS**: {{if .HasFailuresOfType "binary_size"}}Required for large files{{else}}Not currently needed{{end}}

## Success Criteria

✅ **All acceptance criteria completed**  
✅ **Full verification suite passes**  
✅ **Container security constraints validated**  
✅ **Build/test/lint pipeline functional**  
✅ **Git operations working**  
✅ **No files exceed size limits**  

Upon completion, mark `bootstrap_complete = true` in project configuration and proceed with regular development workflow.

---

*Generated by Maestro Bootstrap System - {{.TemplateName}}*