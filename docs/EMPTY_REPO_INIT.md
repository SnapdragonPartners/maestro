# Empty Repository Initialization

## Implementation Status

✅ **COMPLETE** - All components implemented and tested

| Component | Status | Location |
|-----------|--------|----------|
| Empty mirror detection | ✅ Implemented | `pkg/mirror/manager.go:180-196` |
| Empty repository initialization | ✅ Implemented | `pkg/mirror/manager.go:198-300` |
| Integration with EnsureMirror | ✅ Implemented | `pkg/mirror/manager.go:74-85` |
| Temp clone helper | ✅ Implemented | `pkg/workspace/tempclone.go:46-94` |
| Atomic replace helper | ✅ Implemented | `pkg/workspace/tempclone.go:170-214` |
| Pre-create agent directories | ✅ Implemented | `cmd/maestro/main.go:235-254` |
| Workspace setup functions | ✅ Working | `pkg/workspace/{architect,pm}.go` |

## Problem

New GitHub repositories created without initialization have no commits or branches. This causes:
- Mirror clone succeeds but contains no refs
- Workspace clones fail with "Remote branch main not found"
- Agents cannot start

## Solution

Automatically create an initial commit when an empty repository is detected. This:
- Tests GitHub token/authentication immediately
- Eliminates empty-repo edge cases
- Creates proper foundation for normal git workflows
- Allows all workspace clones to succeed

## Implementation Flow

### 1. Empty Mirror Detection

**Location**: `pkg/mirror/manager.go` - `EnsureMirror()` (lines 74-85)

**Status**: ✅ IMPLEMENTED

After creating/updating mirror, check if it's empty:
```go
isEmpty, err := m.isEmptyMirror(mirrorPath)
if err != nil {
    return "", err
}

if isEmpty {
    if err := m.initializeEmptyRepository(ctx, mirrorPath); err != nil {
        return "", fmt.Errorf("failed to initialize empty repository: %w", err)
    }
}
```

### 2. Empty Mirror Check

**Function**: `mirror.Manager.isEmptyMirror(mirrorPath string) (bool, error)` (lines 180-196)

**Status**: ✅ IMPLEMENTED

Check if mirror has any refs:
```bash
# In bare mirror, check for any refs in refs/heads/
ls refs/heads/
# If empty → repository is empty
```

Alternative: `git rev-parse --verify HEAD` will fail if no commits exist.

### 3. Initialize Empty Repository

**Function**: `mirror.Manager.initializeEmptyRepository(ctx, mirrorPath string) error` (lines 198-300)

**Status**: ✅ IMPLEMENTED

**Note**: We don't use `WithTempClone` here because the mirror is empty (nothing to clone). Instead, we create a fresh temp directory and initialize it.

**Steps**:

1. **Create temp workspace** (in `<projectDir>/.tmp/init-<timestamp>/`)
   ```go
   tempDir := filepath.Join(m.projectDir, ".tmp", fmt.Sprintf("init-%d", time.Now().UnixNano()))
   os.MkdirAll(tempDir, 0755)
   defer os.RemoveAll(tempDir) // Automatic cleanup
   ```

2. **Initialize fresh git repository**
   ```bash
   cd <tempDir>
   git init
   git checkout -b main  # Use detected default branch
   ```

3. **Create MAESTRO.md**
   ```go
   content := `# Maestro AI Development Project

This project is managed by Maestro, an AI-powered development orchestrator.

- Architecture: Managed by architect agent
- Implementation: Executed by coder agents
- Quality: Enforced through automated review and testing

For more information, visit: https://github.com/anthropics/maestro
`
   os.WriteFile(filepath.Join(tempDir, "MAESTRO.md"), []byte(content), 0644)
   ```

4. **Commit and push to GitHub**
   ```bash
   git add MAESTRO.md
   git commit -m "Initial Maestro project setup"
   git remote add origin <github-url>  # Read from config.Git.RepoURL
   git push -u origin main
   ```

   **If push fails**: Return error immediately (likely auth failure)

   This validates:
   - GitHub token is valid
   - Repository exists and is accessible
   - Network connectivity works

5. **Update mirror to fetch the new commit**
   ```bash
   cd <mirrorPath>
   git remote update
   ```

6. **Cleanup happens automatically** via defer

### 4. Update Agent Workspaces

**Status**: ✅ WORKING (existing implementation in `pkg/workspace/architect.go` and `pkg/workspace/pm.go`)

**Location**: After mirror is populated, update all agent workspaces atomically

**Helper Functions Available**:
- ✅ `workspace.WithTempClone()` - for safe temporary clones with automatic cleanup (implemented in `pkg/workspace/tempclone.go`)
- ✅ `workspace.AtomicReplace()` - for atomic directory replacement (implemented in `pkg/workspace/tempclone.go`)

**Current Implementation**: The existing workspace setup functions (`EnsureArchitectWorkspace`, `EnsurePMWorkspace`) handle workspace initialization correctly. With empty repository initialization now in place (section 3), workspace clones will always succeed.

**For each agent workspace** (`architect-001/`, `pm-001/`):

1. **Check if workspace needs initialization**
   - If directory doesn't exist → create and clone
   - If directory exists but not a git repo → remove and clone
   - If directory is a valid git repo → skip (already set up)

2. **Clone and atomically replace using helper**
   ```go
   err := workspace.WithTempClone(ctx, projectDir, mirrorPath, workspace.TempCloneOptions{
       Branch: targetBranch,
       Shallow: false, // Full clone for workspaces
   }, func(tempClone string) error {
       // Atomic replacement
       return workspace.AtomicReplace(targetWorkspace, tempClone)
   })
   ```

3. **Atomic replacement implementation**
   ```go
   func AtomicReplace(target, source string) error {
       // 1. Move target out of the way
       oldPath := target + ".old"
       os.Rename(target, oldPath)

       // 2. Move source into place
       os.Rename(source, target)

       // 3. Remove old version
       os.RemoveAll(oldPath)

       return nil
   }
   ```

**Note**: This workspace update is only needed for architect and PM. Coders handle their own workspace setup during their SETUP phase.

**Helper Benefits**:
- ✅ Automatic cleanup even on errors
- ✅ Consistent temp directory location (`<projectDir>/.tmp/`)
- ✅ Context cancellation support
- ✅ Reusable across codebase

### 5. Pre-create All Agent Directories at Startup

**Location**: `cmd/maestro/main.go` - `verifyProject()` (lines 235-254)

**Status**: ✅ IMPLEMENTED

All agent workspace directories are pre-created during project infrastructure verification:

```go
// Pre-create architect and PM directories first
agentDirs := []string{"architect-001", "pm-001"}

// Add coder directories
if cfg.Agents != nil && cfg.Agents.MaxCoders > 0 {
    for i := 1; i <= cfg.Agents.MaxCoders; i++ {
        agentDirs = append(agentDirs, fmt.Sprintf("coder-%03d", i))
    }
}

// Create all directories
for _, dir := range agentDirs {
    agentPath := filepath.Join(projectDir, dir)
    if err := os.MkdirAll(agentPath, 0755); err != nil {
        return fmt.Errorf("failed to create workspace directory %s: %w", dir, err)
    }
}
```

This ensures:
- ✅ Container mounts work (directories exist before agents start)
- ✅ No race conditions during agent startup
- ✅ Consistent workspace structure across all agents

## Error Handling

### GitHub Token Failures
If `git push` fails:
- Log clear error message
- Return error from `initializeEmptyRepository()`
- Fail startup with message: "Failed to initialize empty repository: authentication failed. Check GITHUB_TOKEN"

### Network Failures
If GitHub is unreachable:
- Retry once after 2 seconds
- If still fails, return error
- User can retry startup when network is available

### Permission Failures
If local filesystem operations fail:
- Return clear error about permissions
- Suggest checking directory permissions

## Files NOT Committed

- ❌ `.maestro/config.json` - Contains local path information
- ❌ `.maestro/knowledge.dot` - Would fool detection logic, let coder create it
- ❌ `.maestro/database/` - Runtime data, not source code
- ✅ `MAESTRO.md` - Only file committed in initial setup

## Testing

### Manual Test
1. Create empty GitHub repository (no README, no .gitignore)
2. Configure `config.json` with repo URL
3. Run maestro
4. Verify:
   - Initial commit created
   - `MAESTRO.md` exists in GitHub
   - Mirror has refs
   - All workspaces clone successfully
   - Agents start without errors

### Unit Test
Mock empty mirror detection and verify initialization flow is triggered.

## Future Enhancements

- Add `.gitignore` in initial commit (exclude `.maestro/config.json`, `.maestro/database/`)
- Add `README.md` template with project structure
- Make `MAESTRO.md` content configurable
- Support other git providers (GitLab, Bitbucket) in addition to GitHub
