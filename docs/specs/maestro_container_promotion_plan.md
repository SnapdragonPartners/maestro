# Maestro: Atomic Container Switch + Ephemeral GitHub Auth (Implementation Plan)

**Status:** Ready for unattended implementation by Coder LLM  
**Scope:** DevOps bootstrap & runtime orchestration; credentials bootstrap; atomic container promotion; startup rebuild & reconciliation; tool docs; master template rule; internal runtime state

---

## Decisions (locked)

- **Credentials init:** Use **GitHub CLI Option 2** with network:  
  `echo "$GITHUB_TOKEN" | gh auth login --with-token -h github.com` → `gh auth setup-git` → `gh auth status` → set `user.name`/`user.email` → optional `git ls-remote`.
- **Ephemeral GH state:** Use `GH_CONFIG_DIR=/dev/shm/gh` (fallback `/tmp/gh`) so no secrets persist in the repo or image. Script is **go:embed**’d in the orchestrator and injected at container boot.
- **Mount policy:** Mount mode **matches state**: PLANNING/validation tools → **RO** (`/workspace:ro`), CODING → **RW** (`/workspace:rw`). `/tmp` is writable in both.
- **Atomic promotion:** `container_switch` **promotes** an image atomically **and pins** it on success. On failure, no change to Active or Pin.
- **Crash safety:** On startup, if Active unhealthy or mismatched with Pin, **rollback to top of History** (start fresh container for that image) and **pin to it**. If none, switch to **safe**.
- **Pinned image id:** Stored in **project-level** `.maestro` config (outside repo).  
- **Orchestrator start:** (Non-bootstrap) Rebuild target image, compare **image ID** to `pinned_image_id`, `container_switch` only if changed, then reconcile.
- **Runtime state:** Exactly **one Active** container with a `Role` (`"safe"` or `"target"`), plus a ring-buffer **History**. No CID files in repo; state kept internally in agent/orchestrator memory.
- **LLM rule (master template):**  
  > **Your response must be only tool calls. This app is unattended; any non-tool output will be discarded.**

---

## Story 1 — Embed & run ephemeral **GitHub auth init** at container boot

**Goal:** Non-interactive, ephemeral credentialing for HTTPS pushes using `GITHUB_TOKEN`.  
**Deliverables:** `gh-init` embedded script + injector/runner.

### Code — embedded script (sh) *(no secrets in code)*

> **Path (logical):** `internal/embeds/scripts/gh_init.sh` (embedded); installed/executed inside containers at boot.

```sh
#!/usr/bin/env sh
set -euo pipefail

export GH_CONFIG_DIR="${GH_CONFIG_DIR:-/dev/shm/gh}"
[ -d /dev/shm ] || GH_CONFIG_DIR="/tmp/gh"
mkdir -p "$GH_CONFIG_DIR"
chmod 700 "$GH_CONFIG_DIR"

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GIT_USER_NAME:=Maestro Agent}"
: "${GIT_USER_EMAIL:=maestro-agent@local}"

# Network required
printf '%s' "$GITHUB_TOKEN" | gh auth login --with-token -h github.com
gh auth setup-git
gh auth status -h github.com >/dev/null

git config --global user.name  "$GIT_USER_NAME"
git config --global user.email "$GIT_USER_EMAIL"

# Optional: verify remote if provided (network)
if [ -n "${REPO_URL:-}" ]; then
  git ls-remote --heads "$REPO_URL" >/dev/null
fi

echo "[gh-init] GitHub auth configured (ephemeral: $GH_CONFIG_DIR)"
```

### Code — Go: embed + install + run at boot

> **Paths:** Orchestrator side, used whenever we start a new *candidate* or *active* container.

```go
// internal/embeds/scripts/gh_init.go
package scripts

import _ "embed"

//go:embed gh_init.sh
var GHInitSh []byte
```

```go
// internal/runtime/gh_bootstrap.go
package runtime

import (
	"bytes"
	"context"
	"fmt"
)

type Docker interface {
	Exec(ctx context.Context, cid string, args ...string) ([]byte, error)
	CpToContainer(ctx context.Context, cid string, dstPath string, data []byte, mode int) error
}

func InstallAndRunGHInit(ctx context.Context, d Docker, cid string, repoURL string, script []byte) error {
	// Install script
	if err := d.CpToContainer(ctx, cid, "/usr/local/bin/gh-init", script, 0o755); err != nil {
		return fmt.Errorf("install gh-init: %w", err)
	}
	// Run
	cmd := []string{"sh", "-lc", fmt.Sprintf(`REPO_URL=%q /usr/local/bin/gh-init`, repoURL)}
	if _, err := d.Exec(ctx, cid, cmd...); err != nil {
		return fmt.Errorf("run gh-init: %w", err)
	}
	return nil
}
```

**Acceptance Criteria**
- Starting a new container with `GITHUB_TOKEN` in env succeeds with `gh auth status`.
- No secrets written under `/workspace`; `GH_CONFIG_DIR` exists and is ephemeral.
- `git ls-remote $REPO_URL` succeeds (when `REPO_URL` provided).

---

## Story 2 — **Atomic** `container_switch` (promote & pin)

**Goal:** Start+probe a candidate image, then atomically **activate + pin** on success; on failure, leave system unchanged. Support rollback via History.

### Runtime state (Go)

```go
// internal/state/runtime.go
package state

import "time"

type Role string
const (
	RoleSafe   Role = "safe"
	RoleTarget Role = "target"
)

type ActiveContainer struct {
	Role    Role
	CID     string
	ImageID string // sha256:...
	Name    string
	Started time.Time
}

type HistoryEntry struct {
	Role    Role
	ImageID string
	Name    string
	Started time.Time
	Stopped time.Time
}

type RuntimeState struct {
	Active  *ActiveContainer
	History []HistoryEntry // newest-first ring buffer
}
```

### Switch algorithm (pseudo-Go)

```go
// internal/tools/container_switch.go
func ContainerSwitch(ctx context.Context, role Role, imageID string, o *Orchestrator) (*Result, error) {
	// Resolve candidate imageID if not provided (e.g., last built/tested)
	if imageID == "" && role == RoleTarget {
		imageID = o.LastBuiltOrTestedImageID()
	}
	// Idempotence
	if o.State.Active != nil && o.State.Active.ImageID == imageID && o.Config.PinnedImageID() == imageID {
		return &Result{Status: "noop", ActiveImageID: imageID, Role: string(role)}, nil
	}

	// 1) Prepare: start candidate container
	cid, name, err := o.StartContainer(ctx, role, imageID) // does not touch Active
	if err != nil { return nil, err }

	// 2) Probe: gh-init + basic health checks
	if err := runtime.InstallAndRunGHInit(ctx, o.Docker, cid, o.RepoURL, scripts.GHInitSh); err != nil {
		o.StopContainer(ctx, cid) // cleanup candidate
		return nil, fmt.Errorf("gh-init failed: %w", err)
	}
	if err := o.HealthCheck(ctx, cid); err != nil {
		o.StopContainer(ctx, cid)
		return nil, fmt.Errorf("healthcheck failed: %w", err)
	}

	// 3) Commit (atomic): stop current, push to history, activate candidate, pin
	prev := o.State.Active
	if prev != nil {
		o.HistoryPush(HistoryEntry{
			Role:    prev.Role, ImageID: prev.ImageID, Name: prev.Name,
			Started: prev.Started, Stopped: time.Now(),
		})
		o.StopContainer(ctx, prev.CID)
	}
	o.State.Active = &ActiveContainer{
		Role: role, CID: cid, ImageID: imageID, Name: name, Started: time.Now(),
	}
	if err := o.Config.SetPinnedImageID(imageID); err != nil {
		// Roll back to previous active if pin write fails
		o.StopContainer(ctx, cid)
		if prev != nil {
			if cid2, name2, e2 := o.StartContainer(ctx, prev.Role, prev.ImageID); e2 == nil {
				o.State.Active = &ActiveContainer{Role: prev.Role, CID: cid2, ImageID: prev.ImageID, Name: name2, Started: time.Now()}
				_ = o.Config.SetPinnedImageID(prev.ImageID)
			} else {
				o.State.Active = nil
			}
		} else {
			o.State.Active = nil
		}
		return nil, fmt.Errorf("pin write failed: %w", err)
	}
	return &Result{Status: "switched", ActiveImageID: imageID, Role: string(role)}, nil
}
```

**Acceptance Criteria**
- Success path: Active changes to candidate image; pin updated to same image ID.
- Failure path: Active & pin remain unchanged; candidate cleaned up.
- Idempotence: Re-switching to the same image returns `noop`.
- Rollback supported via History.

---

## Story 3 — Orchestrator startup: **rebuild + reconcile/rollback**

**Goal:** Rebuild deterministically, switch if image changed, ensure Active==Pinned and healthy. Roll back to top of History on mismatch/unhealthy.

### Pseudo-Go

```go
// internal/orchestrator/startup.go
func OnStart(ctx context.Context, o *Orchestrator) error {
	if o.IsBootstrapPhase() { return nil }

	newID, err := o.BuildTargetImage(ctx) // returns image ID
	if err != nil { return err }

	if newID != o.Config.PinnedImageID() {
		if _, err := ContainerSwitch(ctx, state.RoleTarget, newID, o); err != nil {
			return fmt.Errorf("switch to rebuilt image failed: %w", err)
		}
	}

	// Reconcile: ensure Active matches pin and is healthy
	if o.State.Active == nil || o.State.Active.ImageID != o.Config.PinnedImageID() || !o.CheckActiveHealthy(ctx) {
		prev := o.HistoryTop()
		if prev != nil {
			if _, err := ContainerSwitch(ctx, prev.Role, prev.ImageID, o); err != nil {
				if _, e2 := ContainerSwitch(ctx, state.RoleSafe, o.SafeImageID(), o); e2 != nil {
					return fmt.Errorf("rollback failed; safe switch failed: %v / %v", err, e2)
				}
			}
		} else {
			if _, err := ContainerSwitch(ctx, state.RoleSafe, o.SafeImageID(), o); err != nil {
				return fmt.Errorf("safe switch failed: %w", err)
			}
		}
	}
	return nil
}
```

**Acceptance Criteria**
- When rebuild produces a new image ID, system promotes & pins atomically.
- If Active unhealthy or mismatched, system rolls back to `History[0]` and pins to it.
- If no history, system activates **safe** container.

---

## Story 4 — `container_test` semantics & mount policy

**Tool doc (paste into schema/comments)**

```
container_test

Purpose:
  Run the target (or safe) image in a throwaway container to validate environment and/or run tests.
Mounts:
  PLANNING/validation: /workspace mounted READ-ONLY; /tmp is writable.
  CODING: /workspace mounted READ-WRITE; /tmp is writable.
Behavior:
  May compile/run tests with temp paths only; must not modify sources in validation mode.
Returns:
  { "status": "pass" | "fail", "details": { "image_id": "sha256:...", "role": "target|safe", ... } }
Notes:
  Polyglot — do not assume a language. Tests may set caches under /tmp.
  This tool NEVER changes the active container.
```

---

## Story 5 — `container_update` (config-only pin edit; advanced use)

**Tool doc**

```
container_update

Purpose:
  Atomically set the project's pinned target image id in project-level .maestro config.
Input:
  { "pinned_image_id": "sha256:...", "image_tag"?: "...", "reason"?: "...", "dry_run"?: false }
Behavior:
  Validates image exists locally; rejects safe image unless explicitly allowed.
  Writes the new pin id; returns "updated" or "noop".
  Does NOT change the active container; use container_switch to activate.
```

**Code skeleton (Go)**

```go
func ContainerUpdate(ctx context.Context, newID, tag, reason string, dryRun bool, o *Orchestrator) (*Result, error) {
	if !o.ImageExists(ctx, newID) { return nil, fmt.Errorf("image %s not found", newID) }
	old := o.Config.PinnedImageID()
	if old == newID {
		return &Result{Status: "noop", PreviousImageID: old, NewImageID: newID}, nil
	}
	if dryRun {
		return &Result{Status: "would_update", PreviousImageID: old, NewImageID: newID}, nil
	}
	if err := o.Config.SetPinnedImageID(newID); err != nil { return nil, err }
	o.AuditPinChange(old, newID, reason)
	return &Result{Status: "updated", PreviousImageID: old, NewImageID: newID}, nil
}
```

---

## Story 6 — Master template & empty-response guard

**Master template (single line to add at top):**
```
**Your response must be only tool calls. This app is unattended; any non-tool output will be discarded.**
```

**Empty-response handler (guidance tweak):**
- Inject guidance that **names real tools** available in the current phase, e.g.:
  - DevOps: `container_build`, `container_test`, `container_switch`
  - Coding: `shell`, `container_test`, `done`

---

## Story 7 — Template portability micro-diff

Replace BusyBox-incompatible examples like:
```sh
grep -R --include="*.go" pattern .
```
With:
```sh
find /workspace -type f \( -name '*.go' -o -name '*.yml' -o -name '*.yaml' -o -name '*.json' \) -print0 \
| xargs -0 grep -nE 'YOUR_PATTERN' || true
```

---

## Story 8 — Network policy

Default to **network enabled** for container operations; add stricter modes later only if explicitly requested by a tool.

---

## File layout (suggested)

- `internal/embeds/scripts/gh_init.sh` (embedded)  
- `internal/embeds/scripts/gh_init.go` (go:embed)  
- `internal/runtime/gh_bootstrap.go` (installer/runner)  
- `internal/state/runtime.go` (Active/History structs)  
- `internal/tools/container_switch.go` (atomic promotion & pin)  
- `internal/tools/container_update.go` (config-only pin)  
- `internal/orchestrator/startup.go` (rebuild + reconcile/rollback)
