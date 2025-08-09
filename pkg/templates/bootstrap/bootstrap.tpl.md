# Bootstrap Project Setup for {{.ProjectName}}

**Platform**: {{.PlatformDisplayName}} ({{.Platform}})  
**Container**: {{.ContainerImage}}  
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
- [ ] Verify all build targets work: `{{index $defaults "build_command"}}`, `{{index $defaults "test_command"}}`, `{{index $defaults "lint_command"}}`, `{{index $defaults "run_command"}}`
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
- [ ] Ensure rootless execution works with `--user=nobody --read-only --network=none`
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

### Phase 2: High Priority Fixes (Priority 2+)
{{- range .FailuresByPriority}}
{{- if gt .Priority 1}}
1. **{{.Component}}**: {{.Description}}
   - Action: {{.Details.action}}
   {{- if .Details.file_path}}
   - File: {{.Details.file_path}}
   {{- end}}
{{- end}}
{{- end}}

### Phase 3: Validation
1. Run full verification suite: `maestro init --verify`
2. Test complete development workflow:
   - `{{index $defaults "build_command"}}` - should compile successfully
   - `{{index $defaults "test_command"}}` - should run tests
   - `{{index $defaults "lint_command"}}` - should pass linting
   - `{{index $defaults "run_command"}}` - should start application
3. Verify container security constraints
4. Test git operations (clone, worktree, PR creation)

## Technical Notes

### Platform: {{.PlatformDisplayName}}
- **Build System**: {{index $defaults "build_command"}}
- **Package Manager**: {{index $defaults "package_manager"}}
- **Test Framework**: {{index $defaults "test_framework"}}
- **Linting Tool**: {{index $defaults "lint_tool"}}
- **Container Image**: {{.ContainerImage}}

### Container Configuration
{{- if .RequiresNetworkAccess}}
- **Network Access**: Required during setup phase for image pull/build
- **Security**: Will be disabled after setup (`--network=none`)
{{- else}}
- **Network Access**: Disabled (`--network=none`)
{{- end}}
- **User**: Non-root (`--user=nobody`)
- **Filesystem**: Read-only with writable `/tmp`

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