# Bootstrap Spec Refactor: PM Requirements → Architect Technical Spec

## Overview

This document specifies refactoring bootstrap specification generation to cleanly separate **PM requirements** (what's missing) from **Architect technical specification** (how to fix it).

### Current State

PM agent:
1. Detects bootstrap requirements (deterministic code)
2. Stores requirements in state (`StateKeyBootstrapRequirements`)
3. Loads language pack and renders full technical bootstrap spec
4. Injects rendered markdown into `spec_submit` tool via `SetBootstrapMarkdown()`
5. `spec_submit` passes `infrastructure_spec` (markdown string) to architect

### Proposed State

PM agent:
1. Detects bootstrap requirements (deterministic code - no change)
2. Stores requirements in state (no change)
3. Injects **structured requirements** into `spec_submit` via `SetBootstrapRequirements()`
4. `spec_submit` passes `bootstrap_requirements` (typed slice) to architect

Architect agent:
1. Receives structured bootstrap requirements + user spec
2. Loads language pack from config (`config.Project.PrimaryPlatform`)
3. Renders full technical specification from requirements
4. Generates stories for coders

### Key Design Principle

**Bootstrap requirements are transparent to the PM LLM.** The PM LLM never sees or processes bootstrap requirements - they flow through state and orchestrator plumbing. This:
- Removes complexity from PM prompts
- Eliminates a failure mode (LLM mishandling requirements)
- Keeps deterministic logic deterministic

---

## Data Flow

### Current Flow

```
PM detects requirements (deterministic)
    ↓
PM stores in state
    ↓
PM loads language pack
    ↓
PM renders bootstrap.tpl.md → markdown string
    ↓
PM injects via SetBootstrapMarkdown(markdown)
    ↓
spec_submit passes infrastructure_spec (markdown) to architect
    ↓
Architect receives pre-rendered markdown
    ↓
Architect generates stories
```

### Proposed Flow

```
PM detects requirements (deterministic)
    ↓
PM stores in state
    ↓
PM injects via SetBootstrapRequirements([]BootstrapRequirementID)
    ↓
spec_submit passes bootstrap_requirements (typed slice) to architect
    ↓
Architect receives structured requirements
    ↓
Architect loads config + language pack
    ↓
Architect renders bootstrap.tpl.md
    ↓
Architect generates stories
```

---

## Type Definitions

### BootstrapRequirementID (New Type)

```go
// BootstrapRequirementID is a typed identifier for bootstrap requirements.
// Using a string-based type allows JSON serialization while providing type safety.
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

// ValidBootstrapRequirements is the set of valid requirement IDs.
var ValidBootstrapRequirements = map[BootstrapRequirementID]bool{
    BootstrapReqContainer:      true,
    BootstrapReqDockerfile:     true,
    BootstrapReqBuildSystem:    true,
    BootstrapReqKnowledgeGraph: true,
    BootstrapReqGitAccess:      true,
    BootstrapReqBinarySize:     true,
    BootstrapReqExternalTools:  true,
}
```

These map directly to existing `workspace.BootstrapFailureType` values. We may consolidate or alias them.

---

## Interface Changes

### spec_submit Tool (Internal Change Only)

**No change to tool signature** - the LLM-facing interface stays the same:
```go
spec_submit(markdown string, summary string, hotfix bool, maestro_md string)
```

**Internal changes:**

Current:
```go
type SpecSubmitTool struct {
    projectDir        string
    bootstrapMarkdown string  // Pre-rendered markdown
    inFlight          bool
}

func (s *SpecSubmitTool) SetBootstrapMarkdown(markdown string)
```

Proposed:
```go
type SpecSubmitTool struct {
    projectDir            string
    bootstrapRequirements []BootstrapRequirementID  // Structured requirements
    inFlight              bool
}

func (s *SpecSubmitTool) SetBootstrapRequirements(reqs []BootstrapRequirementID)
```

**ProcessEffect data change:**

Current:
```go
Data: map[string]any{
    "infrastructure_spec": infrastructureSpec,  // markdown string
    "user_spec":           markdownStr,
    // ...
}
```

Proposed:
```go
Data: map[string]any{
    "bootstrap_requirements": s.bootstrapRequirements,  // []BootstrapRequirementID
    "user_spec":              markdownStr,
    // ...
}
```

### PM Changes

**File: `pkg/pm/bootstrap.go`**

Remove:
- Language pack loading
- Template rendering (`bootstrap.RenderSpec()`)
- `RenderBootstrapSpec()` function

Keep:
- `BootstrapRequirements` struct (simplified)
- `DetectBootstrapRequirements()` function
- `ContainerStatus` struct (for logging)

**Simplified BootstrapRequirements:**
```go
type BootstrapRequirements struct {
    // Detection results
    NeedsBootstrap        bool
    RequirementIDs        []BootstrapRequirementID

    // Detailed status (for logging/debugging only)
    ContainerStatus       ContainerStatus
    DetectedPlatform      string
    PlatformConfidence    float64

    // Legacy fields (keep for now, deprecate later)
    NeedsDockerfile       bool
    NeedsMakefile         bool
    NeedsKnowledgeGraph   bool
    // ...
}

// ToRequirementIDs converts boolean flags to typed requirement IDs.
func (b *BootstrapRequirements) ToRequirementIDs() []BootstrapRequirementID {
    var ids []BootstrapRequirementID
    if b.NeedsDockerfile {
        ids = append(ids, BootstrapReqDockerfile)
    }
    if b.NeedsMakefile {
        ids = append(ids, BootstrapReqBuildSystem)
    }
    if b.NeedsKnowledgeGraph {
        ids = append(ids, BootstrapReqKnowledgeGraph)
    }
    // ... etc
    return ids
}
```

**File: `pkg/pm/driver.go`**

Change injection call:
```go
// Current
specSubmitTool.SetBootstrapMarkdown(renderedMarkdown)

// Proposed
reqs := d.GetBootstrapRequirements()
if reqs != nil {
    specSubmitTool.SetBootstrapRequirements(reqs.ToRequirementIDs())
}
```

### Architect Changes

**New file: `pkg/architect/bootstrap_spec.go`**

```go
// RenderBootstrapSpec renders the technical bootstrap specification from requirements.
// This is called when architect receives bootstrap_requirements in spec data.
func RenderBootstrapSpec(requirements []BootstrapRequirementID) (string, error) {
    cfg, err := config.GetConfig()
    if err != nil {
        return "", fmt.Errorf("failed to get config: %w", err)
    }

    // Load language pack from config
    platform := cfg.Project.PrimaryPlatform
    if platform == "" {
        platform = "generic"
    }
    pack, _, err := packs.Get(platform)
    if err != nil {
        return "", fmt.Errorf("failed to load pack for %s: %w", platform, err)
    }

    // Convert requirement IDs to BootstrapFailure structs
    failures := requirementIDsToFailures(requirements)

    // Build template data from config
    data := bootstrap.NewTemplateDataWithConfig(
        cfg.Project.Name,
        platform,
        pack.DisplayName,
        cfg.Container.Name,
        cfg.Git.RepoURL,
        cfg.Container.Dockerfile,
        failures,
    )
    if _, err := data.SetPack(); err != nil {
        return "", fmt.Errorf("failed to set pack: %w", err)
    }

    // Render template
    return bootstrap.RenderSpec(data)
}

// requirementIDsToFailures converts typed IDs to BootstrapFailure structs.
func requirementIDsToFailures(ids []BootstrapRequirementID) []workspace.BootstrapFailure {
    var failures []workspace.BootstrapFailure
    for _, id := range ids {
        failure := workspace.BootstrapFailure{
            Type:        idToFailureType(id),
            Component:   string(id),
            Description: idToDescription(id),
            Priority:    idToPriority(id),
        }
        failures = append(failures, failure)
    }
    return failures
}
```

**File: `pkg/architect/driver.go`**

When processing spec submission:
```go
// Check for bootstrap requirements in spec data
if reqs, ok := specData["bootstrap_requirements"].([]BootstrapRequirementID); ok && len(reqs) > 0 {
    // Render bootstrap spec from requirements
    bootstrapSpec, err := RenderBootstrapSpec(reqs)
    if err != nil {
        return fmt.Errorf("failed to render bootstrap spec: %w", err)
    }
    // Prepend to user spec
    fullSpec = bootstrapSpec + "\n\n" + userSpec
}
```

---

## Implementation Plan

### Phase 1: Add Type and Architect Rendering

**Files:**
- `pkg/workspace/bootstrap_types.go` (or add to existing) - `BootstrapRequirementID` type
- `pkg/architect/bootstrap_spec.go` (new) - rendering logic

**Steps:**
1. Define `BootstrapRequirementID` type and constants
2. Create `RenderBootstrapSpec()` function
3. Create `requirementIDsToFailures()` helper
4. Add unit tests for rendering

### Phase 2: Update spec_submit Tool

**Files:**
- `pkg/tools/spec_submit.go`

**Steps:**
1. Change `bootstrapMarkdown string` → `bootstrapRequirements []BootstrapRequirementID`
2. Change `SetBootstrapMarkdown()` → `SetBootstrapRequirements()`
3. Update ProcessEffect data to pass `bootstrap_requirements`
4. Update tests

### Phase 3: Update PM

**Files:**
- `pkg/pm/bootstrap.go`
- `pkg/pm/driver.go`

**Steps:**
1. Add `ToRequirementIDs()` method to `BootstrapRequirements`
2. Remove `RenderBootstrapSpec()` and pack loading
3. Update injection call to use `SetBootstrapRequirements()`
4. Update tests

### Phase 4: Update Architect Spec Processing

**Files:**
- `pkg/architect/driver.go`
- `pkg/architect/spec_processing.go` (or equivalent)

**Steps:**
1. Detect `bootstrap_requirements` in incoming spec data
2. Call `RenderBootstrapSpec()` when present
3. Prepend rendered spec to user spec
4. Update tests

### Phase 5: Cleanup

**Files:**
- `pkg/pm/bootstrap.go`
- `pkg/templates/pm/*.tpl.md`

**Steps:**
1. Remove unused pack-loading code from PM
2. Remove bootstrap-related template references from PM prompts
3. Remove deprecated `bootstrapMarkdown` references

---

## Observability

### Logging Bootstrap Requirements

Log bootstrap requirements at key points for debugging and visibility:

**1. When PM injects requirements (pkg/pm/driver.go):**
```go
reqs := d.GetBootstrapRequirements()
if reqs != nil {
    reqIDs := reqs.ToRequirementIDs()
    if len(reqIDs) > 0 {
        d.logger.Info("📋 Bootstrap requirements detected: %v", reqIDs)
        specSubmitTool.SetBootstrapRequirements(reqIDs)
    }
}
```

**2. When architect receives requirements (pkg/architect/driver.go):**
```go
if reqs, ok := specData["bootstrap_requirements"].([]BootstrapRequirementID); ok && len(reqs) > 0 {
    a.logger.Info("📋 Received bootstrap requirements from PM: %v", reqs)

    bootstrapSpec, err := RenderBootstrapSpec(reqs)
    if err != nil {
        return fmt.Errorf("failed to render bootstrap spec: %w", err)
    }

    // Log rendered spec length for sanity check
    a.logger.Info("📋 Rendered bootstrap spec: %d bytes", len(bootstrapSpec))

    fullSpec = bootstrapSpec + "\n\n" + userSpec
}
```

**3. Optional: Write to temp file for inspection:**
```go
// In architect, after rendering bootstrap spec
if os.Getenv("MAESTRO_DEBUG_BOOTSTRAP") != "" {
    debugPath := filepath.Join(os.TempDir(), "maestro-bootstrap-spec.md")
    os.WriteFile(debugPath, []byte(bootstrapSpec), 0644)
    a.logger.Info("📋 Bootstrap spec written to: %s", debugPath)
}
```

This allows inspection via:
```bash
MAESTRO_DEBUG_BOOTSTRAP=1 maestro run
cat /tmp/maestro-bootstrap-spec.md
```

---

## Migration Notes

### Backward Compatibility

The change is internal - no LLM-facing interface changes. Specs submitted before the change will continue to work because:
1. `infrastructure_spec` (old) and `bootstrap_requirements` (new) can coexist during migration
2. Architect can check for either field and handle appropriately

### Rollback Plan

If issues arise:
1. Revert `SetBootstrapRequirements()` → `SetBootstrapMarkdown()`
2. Restore pack loading in PM
3. Restore template rendering in PM

---

## File Summary

| File | Change Type | Description |
|------|-------------|-------------|
| `pkg/workspace/bootstrap_types.go` | New/Modified | `BootstrapRequirementID` type definition |
| `pkg/architect/bootstrap_spec.go` | New | Bootstrap spec rendering from requirements |
| `pkg/architect/driver.go` | Modified | Call bootstrap renderer when requirements present |
| `pkg/tools/spec_submit.go` | Modified | `bootstrapRequirements` field, `SetBootstrapRequirements()` |
| `pkg/pm/bootstrap.go` | Modified | Add `ToRequirementIDs()`, remove rendering |
| `pkg/pm/driver.go` | Modified | Use `SetBootstrapRequirements()` |

---

## Success Criteria

1. PM no longer loads language packs or renders templates
2. Bootstrap requirements flow as typed slice, not markdown string
3. Architect renders full technical spec from requirements
4. PM LLM never sees bootstrap requirement details
5. All existing bootstrap tests pass
6. No change in coder-received story content

---

## Estimated Effort

- **Phase 1** (Types + Architect rendering): 2-3 hours
- **Phase 2** (spec_submit changes): 1 hour
- **Phase 3** (PM simplification): 1-2 hours
- **Phase 4** (Architect integration): 1-2 hours
- **Phase 5** (Cleanup): 1 hour
- **Testing**: 2-3 hours

**Total: 8-12 hours** (1-2 days)
