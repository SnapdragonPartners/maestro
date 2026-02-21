# Agentsh Integration Spec

## Overview

Integrate [agentsh](https://github.com/canyonroad/agentsh) as a security gateway for AI agent shell execution. Agentsh interposes a policy engine between agents and the host shell via a shell shim, enforcing allow/deny/redirect/audit decisions on every command, file operation, and network connection.

### Why Agentsh

Maestro already has defense-in-depth (read-only defaults, network-disabled defaults, Docker resource limits, non-root execution). Agentsh adds:

- **Runtime policy enforcement** — fine-grained control beyond Docker's coarse capabilities
- **Command-level governance** — deny specific binaries, redirect dangerous operations, audit sensitive ones
- **Subprocess tracking** — monitors the entire process tree, not just the top-level command
- **Redirection over rejection** — steer agents toward safe alternatives instead of hard failures

### Design Principles

- **Transparent to existing code** — the shell shim operates below the executor abstraction
- **Always-on** — all bootstrap containers run with agentsh enforcement
- **Humans in the security loop** — LLMs cannot approve policy exceptions
- **Graceful degradation** — unattended operation degrades to architect guidance, not blocked execution

---

## Phase 1: Proof of Concept

**Goal**: Install agentsh in the bootstrap container, verify it works with existing shell tool, always-on for bootstrap containers.

> **Implementation note**: Phase 1 was implemented with always-on enforcement after testing showed the agentsh shim is transparent enough for production use. The entrypoint uses fail-open semantics so a regular shell is always available as fallback.

### 1.1 Bootstrap Dockerfile Changes

Add agentsh installation to `pkg/dockerfiles/bootstrap.dockerfile`:

```dockerfile
# --- Agentsh security gateway (optional, enabled via config) ---
ARG AGENTSH_VERSION=latest
RUN wget -qO /tmp/agentsh.apk \
      https://github.com/canyonroad/agentsh/releases/download/${AGENTSH_VERSION}/agentsh_${AGENTSH_VERSION}_linux_amd64.apk \
    && apk add --allow-untrusted /tmp/agentsh.apk \
    && rm /tmp/agentsh.apk

# Install shell shim (replaces /bin/sh, /bin/bash with policy-aware wrappers)
# The shim auto-starts the agentsh server on first invocation
RUN agentsh shim install-shell --root / --shim /usr/bin/agentsh-shell-shim --bash

# Copy default policy and server config
COPY configs/agentsh/ /etc/agentsh/
```

The shim replaces `/bin/sh` and `/bin/bash` so that every `sh -c <cmd>` invocation (which is what `ShellTool` and `LongRunningDockerExec` both use) automatically flows through agentsh. No changes to executor code.

### 1.2 Default Policy File

Create `configs/agentsh/default-policy.yaml` with a baseline coder policy:

```yaml
# Coder agent default policy
rules:
  commands:
    # Allow standard development tools
    - match: ["git", "make", "go", "npm", "node", "python*", "pip*"]
      decision: allow

    # Allow file inspection
    - match: ["cat", "ls", "find", "grep", "head", "tail", "wc", "diff", "tree"]
      decision: allow

    # Allow editors and build tools
    - match: ["sed", "awk", "sort", "uniq", "xargs", "tee"]
      decision: allow

    # Audit but allow Docker CLI (for container self-management)
    - match: ["docker"]
      decision: audit
      message: "Docker command executed by agent"

    # Deny destructive system commands
    - match: ["rm -rf /", "mkfs*", "dd if=*of=/dev/*"]
      decision: deny
      message: "Destructive system operation blocked by policy"

    # Deny package manager installs (use container_build instead)
    - match: ["apt install*", "apt-get install*", "apk add*", "yum install*"]
      decision: deny
      message: "Package installation blocked — use container_build tool to modify the container image"

    # Default: allow (permissive baseline, tighten per-project)
    - match: ["*"]
      decision: allow

  network:
    # Allow GitHub for git operations
    - match: ["github.com", "*.github.com"]
      decision: allow

    # Allow common package registries
    - match: ["registry.npmjs.org", "proxy.golang.org", "pypi.org"]
      decision: allow

    # Deny everything else by default
    - match: ["*"]
      decision: deny
      message: "Network access blocked by policy — request an alternative approach"

  files:
    # Protect maestro internals
    - match: ["/workspace/.maestro/database/*"]
      operation: write
      decision: deny
      message: "Cannot modify maestro database files"

    # Allow workspace writes
    - match: ["/workspace/*"]
      decision: allow

    # Allow tmp
    - match: ["/tmp/*"]
      decision: allow
```

This is a starting point. Projects would customize via their `.maestro/agentsh-policy.yaml`.

### 1.3 Configuration Toggle

The `AgentshConfig` struct exists in `pkg/config/config.go` but is **reserved for future Phase 2 use** (custom dev containers where agentsh may be optional). Bootstrap containers always run with agentsh enforcement unconditionally — no config toggle is checked.

```json
{
  "agentsh": {
    "enabled": false,
    "policy_path": ""
  }
}
```

- `enabled` — reserved for Phase 2 custom dev containers (not used for bootstrap)
- `policy_path` — optional override for project-specific policy (future use)

### 1.4 Container Startup Integration

Bootstrap containers start agentsh **unconditionally** via the entrypoint script (`maestro-entrypoint.sh`). The entrypoint installs the shell shim, starts the agentsh server, and uses fail-open semantics — if any step fails, it logs a warning and continues with a regular shell.

Conditional startup logic (checking `AgentshConfig.Enabled`) is reserved for future Phase 2 custom dev containers, where agentsh may be optional depending on project configuration.

### 1.5 Disabling the Shim

Not applicable for bootstrap containers — agentsh is always-on. The entrypoint uses fail-open semantics, so if agentsh fails to start, the container continues with a regular shell. For future custom dev containers (Phase 2), the `AGENTSH_NO_AUTO=1` env var can be used to make the shim pass through transparently.

### 1.6 Verification

- Build bootstrap image with agentsh installed
- Run shell commands through existing `ShellTool` — verify they work identically with agentsh enabled
- Verify denied commands return non-zero exit code + policy message on stderr
- Verify `AGENTSH_NO_AUTO=1` makes it fully transparent
- Run existing integration tests with agentsh enabled — no regressions

### Phase 1 Files Changed

| File | Change |
|------|--------|
| `pkg/dockerfiles/bootstrap.dockerfile` | Add agentsh install + shim |
| `configs/agentsh/default-policy.yaml` | New — baseline policy |
| `configs/agentsh/server-config.yaml` | New — server config |
| `pkg/config/config.go` | Add `Agentsh` config struct |
| `pkg/exec/docker_long_running.go` | Conditional env vars in `StartContainer()` |

**No changes to**: `Executor` interface, `ShellTool`, tool registry, `ToolProvider`, agent wiring, or any templates.

---

## Phase 2: Surfacing Policy Events to the LLM

**Goal**: When agentsh denies or redirects a command, give the LLM enough context to understand what happened and adapt its approach.

### 2.1 The Problem

With phase 1 alone, a denied command looks like this to the LLM:

```json
{
  "stdout": "",
  "stderr": "agentsh: command denied by policy: Package installation blocked — use container_build tool to modify the container image",
  "exit_code": 126
}
```

This works — the LLM can read stderr. But it's mixed in with other stderr output, and the policy guidance message is the most important part. We can do better.

### 2.2 Approach: Post-Process Shell Output

Modify `ShellTool.Execute()` to detect agentsh policy events and surface them prominently. Agentsh uses exit code 126 (permission denied) for denials, and its stderr messages follow a consistent `agentsh: <decision> by policy: <message>` format.

```go
func (s *ShellTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    // ... existing execution logic ...

    result := map[string]any{
        "stdout":    res.Stdout,
        "stderr":    res.Stderr,
        "exit_code": res.ExitCode,
        "cwd":       cwd,
        "command":   cmdStr,
        "duration":  res.Duration.String(),
    }

    // Surface agentsh policy events prominently
    if policyMsg := extractAgentshEvent(res); policyMsg != "" {
        result["policy_event"] = policyMsg
    }

    return marshalJSON(result)
}
```

The `policy_event` field gives the LLM a clean, structured signal separate from noisy stderr.

### 2.3 Alternative: Use agentsh JSON Mode

Instead of parsing stderr, run agentsh in JSON output mode and unwrap the envelope. This gives structured events but changes the output format the executor sees. More invasive — requires the executor to understand agentsh's output schema.

**Recommendation**: Start with stderr parsing (2.2). It's simpler, works with the existing executor, and doesn't require agentsh-specific output mode configuration. Move to JSON mode only if stderr parsing proves unreliable.

### 2.4 Template Updates

Add a section to coder prompt templates explaining policy enforcement:

```
## Security Policy

Your shell commands are governed by a security policy. If a command is denied:
- Read the policy_event field in the tool response for guidance
- Do NOT retry the denied command — it will be denied again
- Follow the policy guidance to find an alternative approach
- If no alternative is apparent, ask the architect for guidance via ask_question
```

This goes in the system prompt so the LLM knows how to interpret and respond to denials.

### Phase 2 Files Changed

| File | Change |
|------|--------|
| `pkg/tools/mcp.go` | Add `extractAgentshEvent()` + surface `policy_event` field |
| `pkg/templates/` | Add policy enforcement section to coder templates |

---

## Phase 3: Graceful Denial Handling with Tiered Escalation

**Goal**: When a command is denied, the coder doesn't just fail — it escalates through a tiered system that tries fast resolution first and degrades gracefully for unattended operation.

### 3.1 Design: Three-Tier Escalation

When a coder encounters a policy denial that it cannot work around on its own:

```
Tier 1: Self-resolution (immediate)
  └─ LLM reads policy_event, tries alternative approach
  └─ Most denials resolve here (e.g., "use container_build instead of apt install")

Tier 2: Human escalation via chat (short timeout)
  └─ Post escalation message to chat with denied command details
  └─ Wait for configurable timeout (default: 30 seconds)
  └─ If human responds → follow their guidance
  └─ If timeout expires → fall through to tier 3

Tier 3: Architect guidance (fallback)
  └─ Ask architect for alternative approach via QUESTION/ANSWER
  └─ Architect suggests policy-compliant path
  └─ Architect CANNOT approve policy exceptions (no security bypass)
```

### 3.2 Why 30 Seconds, Not "A Few Seconds"

A few seconds is too short for a human to read + type a response, even if they're actively watching. 30 seconds is short enough to not meaningfully block a story (which takes minutes to hours) but long enough for a human who happens to be watching to intervene. This should be configurable.

```json
{
  "agentsh": {
    "enabled": false,
    "escalation_timeout_seconds": 30,
    "policy_path": ""
  }
}
```

### 3.3 Tier 2: Chat Escalation

Leverage the existing `chat_post` tool with `post_type: 'escalate'`:

```
[POLICY DENIAL] coder-001 on story #WP-123

Command: psql -h localhost -U admin -d appdb -c "SELECT COUNT(*) FROM users"
Policy rule: Network access to localhost:5432 denied
Guidance: "Database access blocked by policy — request an alternative approach"

Waiting 30s for human guidance. Reply to this message to provide direction,
or the coder will ask the architect for an alternative approach.
```

The WebUI already renders escalation messages prominently and supports replies. No new UI work needed.

### 3.4 Tier 3: Architect Guidance

If the timeout expires, the coder sends a QUESTION to the architect:

```
A shell command was denied by security policy and no human guidance was provided.

Denied command: psql -h localhost -U admin -d appdb -c "SELECT COUNT(*) FROM users"
Policy message: "Database access blocked by policy"

I need to query the database to verify my implementation. What alternative
approach should I use that complies with the security policy?
```

The architect might respond:
- "Use the container_test tool to spin up a temporary container with psql access"
- "Write a Go test that queries the database instead"
- "Mock the database response for now and note it as a manual verification step"

The architect provides a *path forward*, not a policy override.

### 3.5 Implementation: Denial Handler

Add a denial handler that the shell tool invokes when it detects a policy denial:

```go
type DenialHandler struct {
    chatService  *chat.Service
    agentID      string
    storyID      string
    timeout      time.Duration
}

// HandleDenial implements the three-tier escalation.
// Returns guidance string if human/architect provides direction,
// or empty string if the coder should self-resolve.
func (h *DenialHandler) HandleDenial(ctx context.Context, denial PolicyDenial) (string, error) {
    // Tier 2: Post to chat and wait
    h.chatService.Post(ctx, ChatMessage{
        AgentID:  h.agentID,
        PostType: "escalate",
        Content:  formatDenialEscalation(denial),
    })

    // Wait for response with timeout
    guidance, err := h.chatService.WaitForReply(ctx, h.timeout)
    if err == nil && guidance != "" {
        return guidance, nil  // Human responded
    }

    // Tier 3: Return empty — caller (coder) should ask architect
    return "", nil
}
```

The shell tool calls this when it detects a policy denial. If it returns guidance, the tool injects it into the response. If empty, the `policy_event` field tells the coder to ask the architect.

### 3.6 Important Constraint: No Policy Bypass

Neither the human chat response nor the architect guidance actually changes the agentsh policy. They provide *alternative approaches* that the coder can try. If a human genuinely needs to allow a denied command, they must update the policy YAML and (if needed) restart the agentsh server. This is deliberate — runtime policy overrides via chat would undermine the security model.

For the psql example: the human might say "add psql to the allowed commands in your project policy" — which requires a config change, not a runtime bypass.

### 3.7 Offline Policy Refinement

Regardless of how individual denials resolve, aggregate denial data for offline analysis:

- Log all denials to the event log with command, policy rule, resolution tier, and outcome
- Surface denial frequency in the WebUI's session report
- Provide a helper command: `maestro policy-suggestions --session <id>` that analyzes denial logs and suggests policy updates (similar to agentsh's `policy generate` command)

This closes the loop: denials that keep happening indicate the policy needs updating, and humans can make informed updates between runs.

### Phase 3 Files Changed

| File | Change |
|------|--------|
| `pkg/config/config.go` | Add `escalation_timeout_seconds` |
| `pkg/tools/mcp.go` | Integrate `DenialHandler` in shell tool |
| `pkg/tools/denial_handler.go` | New — three-tier escalation logic |
| `pkg/chat/service.go` | Add `WaitForReply()` method (if not already present) |
| `pkg/templates/` | Update coder templates with tier 3 QUESTION pattern |
| `pkg/eventlog/` | Add denial event type for offline aggregation |

---

## Summary

| Phase | Effort | Code Changes | Risk |
|-------|--------|-------------|------|
| 1: PoC | Small | Dockerfile + config + 1 env var | Low — transparent shim, opt-in |
| 2: Events | Small | Shell tool post-processing + templates | Low — additive, no behavior change |
| 3: Escalation | Medium | Denial handler + chat integration + templates | Low-Medium — new control flow in shell tool |

### Dependencies

- Phase 2 depends on Phase 1 (need agentsh running to have events)
- Phase 3 depends on Phase 2 (need events surfaced to trigger escalation)
- All phases are independently shippable — each adds value on its own

### What We Explicitly Don't Do

- **LLM-approved policy exceptions** — humans are the security boundary, not LLMs
- **Agentsh LLM proxy** — our `LLMClient` already handles API calls outside containers
- **Agentsh MCP tool whitelisting** — our `ToolProvider` + registry already controls tool availability
- **Approval via architect** — architect provides guidance, not permission
