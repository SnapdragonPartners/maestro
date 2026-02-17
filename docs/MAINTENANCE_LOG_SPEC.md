# Architect Maintenance Log

## Overview

The maintenance log allows the architect to accumulate observations about issues it encounters during normal operation (code reviews, plan reviews, budget reviews, question handling). These observations are converted into maintenance stories during the next maintenance cycle via an LLM story generation call that normalizes duplicates and clusters related items.

This replaces the pattern of hardcoding a new `FooStory()` function every time a new maintenance pattern is discovered. Hardcoded stories (knowledge sync, docs verification, TODO scan, test coverage, deferred review) continue to exist for baseline hygiene. The maintenance log handles situational issues that arise during operation.

## Data Model

```go
type MaintenanceItem struct {
    Description string    // Free-text from the architect LLM
    Priority    string    // "p1" (urgent), "p2" (normal), "p3" (nice-to-have)
    Source      string    // Auto-set from context, e.g. "coder-001:bfe2c3c5"
    AddedAt     time.Time // Auto-set to time.Now()
}
```

Items are stored in-memory on `MaintenanceTracker.Items` (mutex-protected). They do not persist across restarts.

## Tool: `add_maintenance_item`

### Schema

```json
{
  "name": "add_maintenance_item",
  "description": "Log an issue for the next maintenance cycle.",
  "parameters": {
    "description": {
      "type": "string",
      "required": true,
      "description": "What needs attention"
    },
    "priority": {
      "type": "string",
      "required": true,
      "enum": ["p1", "p2", "p3"],
      "description": "p1=urgent, p2=normal, p3=nice-to-have"
    }
  }
}
```

### Availability

Available to the architect in all review/request states:
- Plan reviews
- Code reviews (iterative approval)
- Budget reviews
- Question handling

Non-terminal tool — calling it does not end the current toolloop. The architect logs the item and continues with its review/decision.

### Auto-populated Fields

- **Source**: Constructed from `agentID:storyID` of the current review context
- **AddedAt**: Set to `time.Now()` when the tool is called

### Example Usage

During a code review, the architect notices binaries are being committed:

```
add_maintenance_item({
  "description": "Project .gitignore is missing rules for Go binaries (bin/), compiled objects (*.o), and OS files (.DS_Store). The done tool's git add -A keeps picking these up.",
  "priority": "p2"
})
```

During a budget review, the architect notices a dependency issue:

```
add_maintenance_item({
  "description": "The project uses an outdated version of chi router (v4) that has known security vulnerabilities. Should upgrade to v5.",
  "priority": "p1"
})
```

## Consumption: LLM-Driven Story Generation

During the maintenance cycle (`runMaintenanceTasks()`):

1. Items are **snapshot and cleared** under mutex lock
2. Items are serialized into a text "spec" document
3. A **single-turn story generation toolloop** runs (same pattern as `handleSpecReview` loop 2):
   - Fresh ContextManager
   - Maintenance-specific prompt with serialized items
   - Prompt instructs: "Normalize duplicates — if the log lists an item multiple times, include that work in one story"
   - LLM calls `submit_stories` with structured requirements
4. Requirements extracted via `convertToolResultToRequirements()`
5. Each requirement converted to a `maintenance.Story` and appended to the spec

### Key Differences from Normal Spec Flow

- **No PM review** — maintenance is internal
- **No persistence** — no spec record, no database writes
- **No container validation** — maintenance runs in the existing container
- **Non-fatal** — if LLM story generation fails, hardcoded stories still run

### Story Properties

- **p1 items** → express stories (skip planning, go straight to coding)
- **p2/p3 items** → normal stories (with planning phase)
- All stories marked `IsMaintenance: true`

## Architecture

### Dependency Injection

The tool uses the `MaintenanceLog` interface (defined in `pkg/tools/maintenance_item.go`):

```go
type MaintenanceLog interface {
    AddMaintenanceItem(item MaintenanceItem)
}
```

The architect's `Driver` struct implements this interface. The reference is injected via the `AgentContext.MaintenanceLog` field (following the `ChatService` precedent).

### Thread Safety

- `MaintenanceTracker.Items` is protected by `MaintenanceTracker.mutex`
- `AddMaintenanceItem()` acquires the lock before appending
- The snapshot+clear in `runMaintenanceTasks()` acquires the lock, copies the slice, sets it to nil, and releases

### Coexistence with NeedsContainerUpgrade

The existing `NeedsContainerUpgrade` / `ContainerUpgradeReason` mechanism is a specialized version of this same concept. Both coexist:
- Container upgrade: set by coder via request metadata, generates `ContainerUpgradeStory()`
- Maintenance log: set by architect via tool call, generates stories via LLM

A future cleanup could migrate container upgrade signaling to use the maintenance log.

## Duplicate Handling

Duplicates are acceptable at the item level. If the architect logs "missing .gitignore" during two different reviews, both items are stored. During the maintenance cycle, the LLM story generation prompt instructs: "Normalize duplicates — if multiple items describe the same issue, create ONE story." The LLM naturally consolidates related items.

## Future Extensions

- **Coder-initiated items**: Coders could signal maintenance needs via request metadata (like container upgrade does today)
- **Persistence**: Items could be persisted to the database for survival across restarts
- **Consolidation**: The LLM already consolidates, but a more sophisticated clustering step could group items before the LLM call
- **Priority-based ordering**: Higher priority stories could be dispatched first
