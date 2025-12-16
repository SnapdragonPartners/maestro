# Bootstrap Architecture Cleanup Specification

## Overview

Clean break from the legacy bootstrap flow architecture. PM becomes the sole authority on bootstrap status, with bootstrap detection moved into the PM package explicitly.

## Current Problems

1. **Legacy `BootstrapFlow`** in `cmd/maestro/flows.go` is deprecated but still present
2. **Multiple bootstrap detection entry points** scattered across kernel, main, PM
3. **Kernel has bootstrap logic** it doesn't need (`isDemoAvailable()` check)
4. **Bootstrap detector lives in `pkg/tools/`** but is really PM-specific logic
5. **Demo availability** is checked in wrong place (kernel vs PM)

## Design Principles

1. **PM is the sole authority** on bootstrap status
2. **Run detection when needed**, not cached (costs of stale data > detection cost)
3. **Demo availability is a flag** that PM manages, WebUI polls
4. **Bootstrap detection triggers**:
   - Spec upload
   - Interview start
   - Story completion signal from architect (if demo flag is false)

## Changes Required

### Phase 1: Remove Legacy Bootstrap Flow

**Files to modify:**
- `cmd/maestro/flows.go` - Remove `BootstrapFlow` struct and related methods
- `cmd/maestro/interactive_bootstrap.go` - Remove or consolidate into PM
- `cmd/maestro/flows_test.go` - Remove `BootstrapFlow` tests
- `cmd/maestro/main.go` - Remove deprecated `-bootstrap` flag entirely

**What to keep:**
- `OrchestratorFlow` becomes the only flow (consider renaming to just `Flow` or keeping as-is)

### Phase 2: Move Bootstrap Detection to PM

**New file:** `pkg/pm/bootstrap.go`
- Move `BootstrapDetector` logic from `pkg/tools/bootstrap_detector.go`
- Expose as PM-specific functionality
- Detection runs against PM workspace (`pm-001/`)

**Files to modify:**
- `pkg/tools/bootstrap_detector.go` - Deprecate or remove (functionality moves to PM)
- `pkg/tools/bootstrap.go` - Update to use PM's bootstrap detection
- `pkg/pm/driver.go` - Use new internal bootstrap detection

### Phase 3: PM Manages Demo Availability

**Add to PM:**
- `demoAvailable bool` flag in PM state
- `IsDemoAvailable() bool` public method
- Update flag on:
  - Spec upload (run detection)
  - Interview start (run detection)
  - Story completion from architect (run detection if flag is false)

**Detection logic:**
```go
func (pm *Driver) runBootstrapDetection() {
    reqs := pm.detectBootstrapRequirements()
    pm.demoAvailable = !reqs.HasAnyMissingComponents()
}
```

**Trigger on story completion:**
```go
func (pm *Driver) handleStoryCompletion(storyID string) {
    // ... existing logic ...

    // Check if bootstrap is now complete
    if !pm.demoAvailable {
        pm.runBootstrapDetection()
    }
}
```

### Phase 4: Simplify Kernel

**Files to modify:**
- `internal/kernel/kernel.go`:
  - Remove `isDemoAvailable()` function
  - Remove bootstrap detection import
  - Demo service created unconditionally (availability determined by PM)
  - Remove conditional `SetDemoService` wiring

**New approach:**
- Kernel always creates demo service
- WebUI always gets demo service reference
- PM controls whether demo is actually available via flag

### Phase 5: WebUI Queries PM for Demo Status

**Files to modify:**
- `pkg/webui/demo_handlers.go` - Query PM for availability
- `pkg/webui/server.go` - Add PM reference for demo queries

**New endpoint behavior:**
```go
func (s *Server) handleDemoStatus(w http.ResponseWriter, r *http.Request) {
    // Check with PM if demo is available
    if !s.pmAgent.IsDemoAvailable() {
        // Return unavailable status (not 503, just status: unavailable)
        json.NewEncoder(w).Encode(map[string]any{
            "available": false,
            "reason": "Bootstrap not complete",
        })
        return
    }

    // ... existing demo status logic ...
}
```

## Files Summary

### Remove entirely:
- (none - prefer deprecation/consolidation over deletion)

### Major changes:
| File | Change |
|------|--------|
| `cmd/maestro/flows.go` | Remove `BootstrapFlow`, keep `OrchestratorFlow` |
| `cmd/maestro/interactive_bootstrap.go` | Remove or consolidate |
| `internal/kernel/kernel.go` | Remove `isDemoAvailable()`, simplify demo setup |
| `pkg/pm/driver.go` | Add demo availability flag and triggers |
| `pkg/webui/demo_handlers.go` | Query PM for availability |

### New files:
| File | Purpose |
|------|---------|
| `pkg/pm/bootstrap.go` | Bootstrap detection logic (moved from tools) |

### Deprecate:
| File | Reason |
|------|--------|
| `pkg/tools/bootstrap_detector.go` | Logic moves to `pkg/pm/bootstrap.go` |

## Migration Notes

1. **No breaking changes to CLI** - `-bootstrap` flag already deprecated/ignored
2. **WebUI behavior unchanged** - Still polls, still shows availability
3. **PM behavior enhanced** - Now explicitly manages demo availability
4. **Kernel simplified** - No longer needs to understand bootstrap

## Testing

1. Verify demo becomes available after bootstrap story completes
2. Verify demo stays unavailable if bootstrap requirements exist
3. Verify WebUI correctly reflects demo availability
4. Verify spec upload triggers fresh detection
5. Verify interview start triggers fresh detection

## Acceptance Criteria

- [ ] `BootstrapFlow` removed from codebase
- [ ] `-bootstrap` flag removed from CLI
- [ ] Bootstrap detection lives in `pkg/pm/bootstrap.go`
- [ ] PM exposes `IsDemoAvailable()` method
- [ ] PM updates demo flag on spec upload, interview start, story completion
- [ ] Kernel has no bootstrap logic
- [ ] WebUI queries PM for demo availability
- [ ] All existing tests pass
- [ ] Demo mode works correctly after bootstrap completes
