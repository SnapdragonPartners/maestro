# Bootstrap Spec Refactor: PM Requirements → Architect Technical Spec

## Status: IMPLEMENTED

All phases completed. Ready for production testing.

**Commits:**
| Commit | Phase | Description |
|--------|-------|-------------|
| `5add4ad` | 1 | Types and architect rendering |
| `d0a1883` | 2-3 | spec_submit and PM updates |
| `2759681` | 4 | Architect spec processing |
| `fa51a58` | 5 | Cleanup (state key conflict fix) |
| `977631c` | Tests | Comprehensive test coverage |

---

## Overview

This document specifies refactoring bootstrap specification generation to cleanly separate **PM requirements** (what's missing) from **Architect technical specification** (how to fix it).

### Previous State

PM agent:
1. Detects bootstrap requirements (deterministic code)
2. Stores requirements in state (`StateKeyBootstrapRequirements`)
3. Loads language pack and renders full technical bootstrap spec
4. Injects rendered markdown into `spec_submit` tool via `SetBootstrapMarkdown()`
5. `spec_submit` passes `infrastructure_spec` (markdown string) to architect

### Current State (After Refactor)

PM agent:
1. Detects bootstrap requirements (deterministic code - no change)
2. Stores detection struct in state (no change)
3. Injects **structured requirements** into `spec_submit` via `SetBootstrapRequirements()`
4. `spec_submit` passes `bootstrap_requirements` (typed slice) to architect

Architect agent:
1. Receives structured bootstrap requirements + user spec
2. Loads language pack from config (`config.Project.PrimaryPlatform`)
3. Renders full technical specification from requirements
4. Generates stories for coders

### Key Design Principle

**Separation of config deficits vs technical prerequisites:**

The PM's relationship to bootstrap is split into two categories:

**A) Config deficits (PM-visible, PM-owned):**
- Project name
- Git repository URL
- Platform / language choice

These are "user-facing config settings" that only the user can provide. The PM LLM is allowed to:
- See signals indicating these are missing (`NeedsProjectConfig`, `NeedsGitRepo`)
- Ask the user for this information
- Call the `bootstrap` MCP tool to store the values in config

**B) Technical bootstrap prerequisites (architect-owned, opaque to PM LLM):**
- Dockerfile / container setup
- Makefile targets
- Knowledge graph initialization
- Claude Code installation
- .gitignore creation

These flow as **requirement IDs** through orchestrator plumbing. The PM LLM never sees the details - only the architect renders the full technical specification.

### Bootstrap Tool as Validation Point

The `bootstrap` MCP tool (`pkg/tools/bootstrap.go`) is the interface between PM LLM and config. It:
- Validates and normalizes the `platform` parameter using `packs.NormalizePlatform()`
- Accepts common aliases (e.g., "golang" → "go")
- **Always stores a valid pack ID** - either a canonical name with an available pack, or "generic" as fallback
- Lists valid platform options in its tool definition for LLM awareness

**Platform normalization is defined in `pkg/templates/packs/packs.go`:**
- Canonical platform IDs = pack JSON filenames (currently: `go`, `generic`)
- Alias mapping handles common variations (e.g., "golang" → "go")
- `NormalizePlatform()` **guarantees** config only stores valid pack IDs:
  - Known aliases with packs → canonical name (e.g., "golang" → "go")
  - Aliases without packs → "generic" (e.g., "python" → "generic", since no python.json exists yet)
  - Unknown platforms → "generic"
- `packs.ListAvailable()` returns available packs
- `packs.GetPlatformList()` returns only platforms with actual packs (excludes "generic")

---

## Implementation Details

### Files Changed

| File | Change Type | Description |
|------|-------------|-------------|
| `pkg/workspace/bootstrap_requirements.go` | **New** | `BootstrapRequirementID` type, validation, conversion helpers |
| `pkg/workspace/bootstrap_requirements_test.go` | **New** | Unit tests for requirement ID handling |
| `pkg/architect/bootstrap_spec.go` | **New** | `RenderBootstrapSpec()` function |
| `pkg/architect/bootstrap_spec_test.go` | **New** | 14 unit tests for spec rendering |
| `pkg/architect/request_spec.go` | Modified | Receives requirements, calls renderer |
| `pkg/tools/spec_submit.go` | Modified | `SetBootstrapRequirements()` method |
| `pkg/pm/bootstrap.go` | Modified | Added `ToRequirementIDs()` method |
| `pkg/pm/working.go` | Modified | Injects requirements, sends to architect |
| `pkg/proto/unified_protocol.go` | Modified | Added `BootstrapRequirements` field to payload |

### Type Definitions

**File: `pkg/workspace/bootstrap_requirements.go`**

```go
// BootstrapRequirementID is a typed identifier for bootstrap requirements.
type BootstrapRequirementID string

const (
    BootstrapReqContainer      BootstrapRequirementID = "container"
    BootstrapReqDockerfile     BootstrapRequirementID = "dockerfile"
    BootstrapReqBuildSystem    BootstrapRequirementID = "build_system"
    BootstrapReqKnowledgeGraph BootstrapRequirementID = "knowledge_graph"
    BootstrapReqGitAccess      BootstrapRequirementID = "git_access"
    BootstrapReqBinarySize     BootstrapRequirementID = "binary_size"
    BootstrapReqExternalTools  BootstrapRequirementID = "external_tools"
)

// IsValidRequirementID checks if a requirement ID is valid.
func IsValidRequirementID(id BootstrapRequirementID) bool

// RequirementIDToFailureType maps a requirement ID to a failure type.
func RequirementIDToFailureType(id BootstrapRequirementID) BootstrapFailureType

// RequirementIDsToFailures converts requirement IDs to BootstrapFailure structs.
func RequirementIDsToFailures(ids []BootstrapRequirementID) []BootstrapFailure
```

### PM Changes

**File: `pkg/pm/bootstrap.go`**

Added `ToRequirementIDs()` method to `BootstrapRequirements`:

```go
func (r *BootstrapRequirements) ToRequirementIDs() []workspace.BootstrapRequirementID {
    var ids []workspace.BootstrapRequirementID

    // Container-related requirements
    if r.ContainerStatus.IsBootstrapFallback && !r.ContainerStatus.HasValidContainer {
        ids = append(ids, workspace.BootstrapReqContainer)
    }
    if r.NeedsDockerfile {
        ids = append(ids, workspace.BootstrapReqDockerfile)
    }
    if r.NeedsMakefile {
        ids = append(ids, workspace.BootstrapReqBuildSystem)
    }
    if r.NeedsKnowledgeGraph {
        ids = append(ids, workspace.BootstrapReqKnowledgeGraph)
    }
    if r.NeedsGitRepo {
        ids = append(ids, workspace.BootstrapReqGitAccess)
    }

    return ids
}
```

**File: `pkg/pm/working.go`**

Requirements injection in `getWorkingTools()`:
```go
if reqs := d.GetBootstrapRequirements(); reqs != nil && reqs.HasAnyMissingComponents() {
    reqIDs := reqs.ToRequirementIDs()
    if len(reqIDs) > 0 {
        submitTool.SetBootstrapRequirements(reqIDs)
        d.logger.Info("📋 Injected bootstrap requirements into spec_submit: %v", reqIDs)
    }
}
```

Requirements sent in `sendSpecApprovalRequest()`:
```go
var bootstrapReqs []string
if reqs := d.GetBootstrapRequirements(); reqs != nil && reqs.HasAnyMissingComponents() {
    for _, id := range reqs.ToRequirementIDs() {
        bootstrapReqs = append(bootstrapReqs, string(id))
    }
}
```

### Architect Changes

**File: `pkg/architect/bootstrap_spec.go`**

```go
func RenderBootstrapSpec(requirements []workspace.BootstrapRequirementID, logger *logx.Logger) (string, error) {
    // Log received requirements
    logger.Info("Received bootstrap requirements: %v", requirements)

    cfg, err := config.GetConfig()
    if err != nil {
        return "", fmt.Errorf("failed to get config: %w", err)
    }

    // Determine platform from config
    platform := "generic"
    if cfg.Project != nil && cfg.Project.PrimaryPlatform != "" {
        platform = cfg.Project.PrimaryPlatform
    }

    // Load language pack
    pack, warnings, err := packs.Get(platform)
    // ... render template using pack and requirements
}
```

**File: `pkg/architect/request_spec.go`**

Architect receives and renders requirements:
```go
if len(approvalPayload.BootstrapRequirements) > 0 {
    reqIDs := make([]workspace.BootstrapRequirementID, 0, len(approvalPayload.BootstrapRequirements))
    for _, id := range approvalPayload.BootstrapRequirements {
        reqIDs = append(reqIDs, workspace.BootstrapRequirementID(id))
    }

    d.logger.Info("📋 Received bootstrap requirements from PM: %v", reqIDs)

    rendered, err := RenderBootstrapSpec(reqIDs, d.logger)
    if err != nil {
        d.logger.Warn("Failed to render bootstrap spec: %v (continuing without bootstrap)", err)
    } else {
        infrastructureSpec = rendered
    }
}
```

### Proto Changes

**File: `pkg/proto/unified_protocol.go`**

```go
type ApprovalRequestPayload struct {
    // ...
    InfrastructureSpec    string   `json:"infrastructure_spec,omitempty"`    // DEPRECATED
    BootstrapRequirements []string `json:"bootstrap_requirements,omitempty"` // NEW: Requirement IDs
    // ...
}
```

---

## Data Flow (Implemented)

```
PM detects requirements (deterministic)
    ↓
PM stores detection struct in state (StateKeyBootstrapRequirements)
    ↓
PM calls getWorkingTools() which injects requirements:
    submitTool.SetBootstrapRequirements(reqs.ToRequirementIDs())
    ↓
spec_submit stores requirements in ProcessEffect.Data["bootstrap_requirements"]
    ↓
PM calls sendSpecApprovalRequest() which gets requirements from detection struct:
    reqs.ToRequirementIDs() → payload.BootstrapRequirements
    ↓
Architect receives REQUEST message with BootstrapRequirements field
    ↓
Architect calls RenderBootstrapSpec(reqIDs, logger)
    - Loads config
    - Loads language pack
    - Converts IDs to BootstrapFailure structs
    - Renders bootstrap.tpl.md
    ↓
Architect prepends rendered spec to user spec
    ↓
Architect generates stories from combined spec
```

---

## Observability

### Log Messages

**PM logs:**
```
📋 Injected bootstrap requirements into spec_submit: [dockerfile build_system knowledge_graph]
📋 Stored spec for preview (bootstrap reqs: 3, user: 1234 bytes, hotfix: false, summary: ...)
```

**Architect logs:**
```
📋 Received bootstrap requirements from PM: [dockerfile build_system knowledge_graph]
Received bootstrap requirements: [dockerfile build_system knowledge_graph]
Rendered bootstrap spec: 9663 bytes
```

### Debug Output

Set `MAESTRO_DEBUG_BOOTSTRAP=1` to write rendered spec to temp file:

```bash
MAESTRO_DEBUG_BOOTSTRAP=1 maestro run
cat /tmp/maestro-bootstrap-spec.md
```

---

## Test Coverage

### Unit Tests Added

**`pkg/workspace/bootstrap_requirements_test.go`** (148 lines):
- `TestIsValidRequirementID` - 9 cases for ID validation
- `TestRequirementIDToFailureType` - 8 cases for type mapping
- `TestRequirementIDsToFailures` - 4 cases for conversion
- `TestRequirementIDsToFailures_FieldValues` - Validates output fields

**`pkg/pm/bootstrap_test.go`** (added ~180 lines):
- `TestBootstrapRequirements_ToRequirementIDs` - 9 test cases:
  - Empty requirements
  - Container fallback without valid container
  - Container fallback with valid container (no requirement)
  - Dockerfile only
  - Makefile only
  - Knowledge graph only
  - Git repo only
  - Multiple requirements
  - All requirements
- `TestBootstrapRequirements_ToRequirementIDs_Idempotent`
- `TestBootstrapRequirements_ToRequirementIDs_AllIDsAreValid`

**`pkg/architect/bootstrap_spec_test.go`** (new, ~400 lines):
- `TestRenderBootstrapSpec_EmptyRequirements`
- `TestRenderBootstrapSpec_SingleRequirement` - 5 cases (container, dockerfile, build_system, knowledge_graph, git_access)
- `TestRenderBootstrapSpec_MultipleRequirements`
- `TestRenderBootstrapSpec_DifferentPlatforms` - 5 platforms (go, python, node, rust, generic)
- `TestRenderBootstrapSpec_NilLogger`
- `TestRenderBootstrapSpec_NoConfig`
- `TestRenderBootstrapSpec_MinimalConfig`
- `TestRenderBootstrapSpec_DebugOutput`
- `TestRenderBootstrapSpec_ProjectConfigValues`
- `TestRenderBootstrapSpec_InvalidRequirementFiltered`
- `TestRenderBootstrapSpec_SpecSize`

### Coverage Improvement

- `pkg/architect`: 22.3% → 24.1% (+1.8%)
- `pkg/pm`: 25.0% → 26.1% (+1.1%)
- `pkg/workspace`: Already had 11.6%

---

## Production Testing Checklist

### Pre-requisites
- [ ] All unit tests pass (`make test`)
- [ ] Lint passes (`make lint`)
- [ ] Build succeeds (`make build`)

### Scenario 1: Greenfield Project (Full Bootstrap)

**Setup:** Empty directory, no config, no Dockerfile, no Makefile

**Expected behavior:**
1. PM detects missing components: container, dockerfile, build_system, knowledge_graph
2. PM calls `spec_submit` with user spec
3. `spec_submit` includes `bootstrap_requirements: [container, dockerfile, build_system, knowledge_graph]`
4. Architect receives requirements and logs: `📋 Received bootstrap requirements from PM: [container dockerfile build_system knowledge_graph]`
5. Architect renders full technical spec (~10KB)
6. Architect generates bootstrap stories

**Verify:**
- [ ] PM logs show `📋 Injected bootstrap requirements into spec_submit: [...]`
- [ ] Architect logs show `📋 Received bootstrap requirements from PM: [...]`
- [ ] Architect logs show `Rendered bootstrap spec: XXXXX bytes`
- [ ] Generated stories include Dockerfile creation, Makefile creation, etc.
- [ ] With `MAESTRO_DEBUG_BOOTSTRAP=1`, check `/tmp/maestro-bootstrap-spec.md` contains full spec

### Scenario 2: Partial Bootstrap (Missing Makefile)

**Setup:** Project with Dockerfile but no Makefile

**Expected behavior:**
1. PM detects: `build_system` requirement only
2. Architect receives single requirement
3. Architect renders smaller spec (~5KB)

**Verify:**
- [ ] Only `build_system` in requirements
- [ ] Rendered spec focuses on Makefile

### Scenario 3: No Bootstrap Needed

**Setup:** Fully configured project (Dockerfile, Makefile, knowledge graph exist)

**Expected behavior:**
1. PM detects no missing components
2. No bootstrap requirements injected
3. Architect receives user spec only (no `bootstrap_requirements`)
4. Architect does NOT log "Received bootstrap requirements"

**Verify:**
- [ ] No bootstrap-related logs in architect
- [ ] Spec processing works normally

### Scenario 4: Hotfix Mode

**Setup:** Development in progress (`in_flight=true`)

**Expected behavior:**
1. Hotfix submission (`hotfix=true`) should NOT include bootstrap requirements
2. Bootstrap requirements are only for initial spec submissions

**Verify:**
- [ ] Hotfix submissions have empty/nil `bootstrap_requirements`

### Scenario 5: Platform-Specific Rendering

**Setup:** Projects with different `primary_platform` values

**Expected behavior:**
1. Go project → Go pack loaded, Go-specific content in spec
2. Generic project → Generic pack loaded

**Verify:**
- [ ] Platform appears in rendered spec
- [ ] Pack-specific content (e.g., Go build commands) appears

### Scenario 6: Error Handling

**Test cases:**
- [ ] Missing config → `RenderBootstrapSpec` returns error, architect logs warning, continues without bootstrap
- [ ] Invalid requirement ID → Filtered out by `RequirementIDsToFailures`
- [ ] Unknown platform → Falls back to generic pack with warning

---

## Rollback Plan

If issues arise in production:

1. Revert commits in reverse order:
   ```bash
   git revert 977631c fa51a58 2759681 d0a1883 5add4ad
   ```

2. Or cherry-pick fix: The `InfrastructureSpec` field is still present (marked deprecated) so old-style payloads would still work if needed.

---

## Success Criteria

All criteria met:

1. ✅ PM no longer loads language packs or renders templates
2. ✅ Bootstrap requirements flow as typed slice, not markdown string
3. ✅ Architect renders full technical spec from requirements
4. ✅ PM LLM never sees bootstrap requirement details
5. ✅ All existing bootstrap tests pass
6. ⏳ No change in coder-received story content (verify in production)

---

## Future Improvements

1. **Remove deprecated `InfrastructureSpec` field** - After confirming production works, remove the deprecated field from `ApprovalRequestPayload`

2. **Add integration tests** - Full PM → Architect flow with mock LLM

3. **Add more requirement types** - `BootstrapReqClaudeCode`, `BootstrapReqGitignore` are defined but not yet used in `ToRequirementIDs()`
