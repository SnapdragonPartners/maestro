# Phase 5 Tooling Stories — File & GitHub MCP Tools

These stories add two specialized tools to the existing MCP framework:

1. **FileTool** for efficient multi‑file read/write and diff operations.
2. **GitHubTool** for PR creation, update, and merging.

Both tools plug into the `pkg/tools` registry and expose a clear JSON schema, enabling coding and architect agents to invoke them via MCP tags.

Front‑matter schema remains unchanged.

## Table of Contents

| ID  | Title                                      | Est. | Depends |
| --- | ------------------------------------------ | ---- | ------- |
| 050 | FileTool implementation & integration      | 3    | 047     |
| 051 | FileTool unit tests & mock fixtures        | 2    | 050     |
| 052 | GitHubTool implementation (PR workflow)    | 4    | 047     |
| 053 | GitHubTool unit tests & mock GitHub server | 2    | 052     |

---

### Story 050 — FileTool implementation & integration

````markdown
---
id: 050
title: "FileTool implementation & integration"
depends_on: [047]
est_points: 3
---
**Task**  
Create `pkg/tools/file.go` implementing a `FileTool` with the following schema:
```jsonc
{
  "action": "read" | "write" | "append" | "diff",
  "path": "relative/file/path",
  "content": "<string>"        // required for write/append
}
````

Behavior:

1. **read** → returns `{ "content": <file text> }`.
2. **write** → overwrites file; returns `{ "bytes": n }`.
3. **append** → appends to file; returns `{ "bytes": n }`.
4. **diff** → returns unified diff vs. supplied `content`.

Register under MCP name `file` and document usage in `docs/tools.md`.

**Acceptance Criteria**

* `FileTool` handles read/write/append/diff actions correctly.
* Tool respects repository root confinement (no `../`).
* Added to global registry and discoverable via `tools.Get("file")`.

````

### Story 051 — FileTool unit tests & mock fixtures
```markdown
---
id: 051
title: "FileTool unit tests & mock fixtures"
depends_on: [050]
est_points: 2
---
**Task**  
Add table‑driven tests in `pkg/tools/file_test.go`:
1. Create temp directory with sample files.
2. Verify each action (`read`, `write`, `append`, `diff`) returns expected JSON.
3. Ensure path traversal (`../`) is rejected.

**Acceptance Criteria**
* Tests cover all actions and edge cases.
* `go test ./...` passes.
````

### Story 052 — GitHubTool implementation (PR workflow)

````markdown
---
id: 052
title: "GitHubTool implementation (PR workflow)"
depends_on: [047]
est_points: 4
---
**Task**  
Create `pkg/tools/github.go` that interfaces with GitHub via the REST API (or `gh` CLI if available).  Schema:
```jsonc
{
  "action": "create_pr" | "update_pr" | "merge_pr",
  "repo": "owner/repo",
  "base": "main",
  "head": "feature-branch",
  "title": "<string>",
  "body": "<markdown>",
  "pr_number": 123          // required for update/merge
}
````

Behavior:

1. Reads a GitHub token from env `GITHUB_TOKEN` or config.
2. On success, returns `{ "pr_number": n, "url": "https://github.com/..." }`.
3. Errors include HTTP status and message.

Register under MCP name `github`.

**Acceptance Criteria**

* Live call succeeds when `GITHUB_TOKEN` and repo exist (integration test flag `--live`).
* In mock mode, tool returns canned JSON without network call.

````

### Story 053 — GitHubTool unit tests & mock GitHub server
```markdown
---
id: 053
title: "GitHubTool unit tests & mock GitHub server"
depends_on: [052]
est_points: 2
---
**Task**  
Add tests in `pkg/tools/github_test.go`:
1. Spin up `httptest` server mimicking GitHub API endpoints for PR create/update/merge.
2. Inject mock server URL via env `GITHUB_API_BASE`.
3. Verify requests contain proper headers (`Authorization`), JSON body, and response parsing.

**Acceptance Criteria**
* All GitHubTool actions succeed against mock server.
* Live tests skipped unless `-live` flag and env token set.
````

---

> **Generated:** 2025-06-11

