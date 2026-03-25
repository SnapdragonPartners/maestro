# Plan: Unified Password with Verifier File

## Problem

When `MAESTRO_PASSWORD` is not set, Maestro generates a random password on each run. Secrets encrypted with a previous session's password become undecryptable on restart, effectively bricking the secrets file. The only protection is a terminal banner telling users to save the password — easy to miss, especially for the less-technical users who are most likely to use WebUI-based setup.

## Context: Deliberate Product Direction Change

The current codebase intentionally hard-fails if `secrets.json.enc` exists but `MAESTRO_PASSWORD` is not set. That was the right call when the only way to add secrets was via env vars — it prevented silent data loss from ephemeral password drift.

However, setup mode (PR #149) changed the product model: users who aren't comfortable with the command line can now enter API keys through the WebUI. These users never set `MAESTRO_PASSWORD`, so the hard-fail bricks them on restart. This plan deliberately reverses the "env var required" stance for the WebUI-driven flow, while preserving it as the power-user path.

## Design

### Core Idea

Generate the unified password **once**, store only a **scrypt verifier** on disk (never the password itself), and recover the password on subsequent runs via **Basic Auth login** to the WebUI. The existing `WaitForSetup` gate naturally blocks all secret-dependent operations (agent creation, API key access) until the password is recovered.

### Password Lifecycle

```
First Run (no MAESTRO_PASSWORD, no verifier file, no secrets file):
  1. Generate random password
  2. Display prominently in terminal
  3. Write scrypt verifier to .maestro/.password-verifier.json (0600, atomic)
  4. Use password in-memory for WebUI auth + secrets encryption

Subsequent Run (no MAESTRO_PASSWORD, verifier file exists):
  1. Detect verifier file -> do NOT generate a new password
  2. Start WebUI without password in memory
  3. User logs in via Basic Auth -> browser sends password
  4. Verify against .password-verifier.json -> if valid, cache in memory
  5. Decrypt secrets file using now-available password
  6. WaitForSetup unblocks (API keys now in memory) -> agents start

Any Run (MAESTRO_PASSWORD set):
  1. Use env var directly (current behavior, unchanged)
  2. Skip verifier file entirely for auth purposes
```

### Startup Behavior: What Runs Before Password Recovery

When the verifier file exists but no `MAESTRO_PASSWORD` is set:
- **Starts:** WebUI server (serves login page and setup page)
- **Blocks:** Agent creation, API key access, secrets decryption, all secret-dependent operations
- **Mechanism:** `WaitForSetup` already blocks on `CheckRequiredAPIKeys()`. Without the password, secrets can't be decrypted, so API keys from the secrets file aren't in memory, so setup mode activates. Once the user logs in (password recovered, secrets decrypted), keys appear and the gate opens.

No new gate or synchronization mechanism is needed — the existing `WaitForSetup` covers this.

### Precedence

1. `MAESTRO_PASSWORD` env var (always wins)
2. In-memory project password (recovered via Basic Auth during session)
3. Verifier file exists -> accept password via Basic Auth, verify + cache
4. No env var + no verifier file + no secrets file -> generate, display, store verifier

### Migration / Edge Cases

| Verifier File | Secrets File | MAESTRO_PASSWORD | Behavior |
|:---:|:---:|:---:|---|
| No | No | No | **First run.** Generate password, create verifier, display banner. |
| No | No | Yes | Use env var. Create verifier from it (so future runs without env var work). |
| Yes | No | No | Start WebUI, wait for login. No secrets to decrypt; password recovered for session use. |
| Yes | Yes | No | Start WebUI, wait for login. On successful auth, decrypt secrets. |
| Yes | Yes | Yes | Use env var immediately. Decrypt secrets at startup. (Current behavior.) |
| Yes | No | Yes | Use env var. No secrets to decrypt. |
| **No** | **Yes** | **No** | **Legacy/migration.** Secrets file exists but no verifier — this means the project was created before the verifier feature. **Hard-fail** requiring `MAESTRO_PASSWORD`, same as today. We cannot generate a new password (it wouldn't decrypt existing secrets) and we have no verifier to check against. |
| No | Yes | Yes | Use env var. Decrypt secrets. Create verifier from env var password. |

The key migration rule: **a secrets file without a verifier always requires `MAESTRO_PASSWORD`.** The verifier file is only created going forward (on first password generation or when `MAESTRO_PASSWORD` is set for the first time). This ensures no existing deployment breaks.

### Accepted Tradeoff

If the user loses the generated password and has no `MAESTRO_PASSWORD` env var, both `.password-verifier.json` and `secrets.json.enc` are bricked. Recovery path: delete both files and start fresh. This is documented in the terminal banner, README, and WebUI.

## Implementation

### Phase 1: Password Verifier File

**New file: `pkg/config/password_verifier.go`**

Uses scrypt (already imported for secrets encryption) for consistency.

```go
// Verifier file: .maestro/.password-verifier.json
// Format: {"version":1,"salt":"<base64>","hash":"<base64>"}
//
// salt: 32 random bytes
// hash: scrypt(password, salt, N=32768, r=8, p=1, keyLen=32)
// Same scrypt parameters as secrets.go for consistency.

const passwordVerifierFile = ".password-verifier.json"

type passwordVerifier struct {
    Version int    `json:"version"`
    Salt    string `json:"salt"`    // base64-encoded
    Hash    string `json:"hash"`    // base64-encoded scrypt output
}

func SavePasswordVerifier(projectDir, password string) error
func VerifyPassword(projectDir, password string) (bool, error)
func PasswordVerifierExists(projectDir string) bool
```

**Atomic file write:**
- Create temp file in `.maestro/` (e.g., `.password-verifier.json.tmp`)
- Write JSON content
- `fsync` the file descriptor
- `os.Rename` temp -> final path
- This prevents half-written verifier files that would block password generation while being unverifiable

**New file: `pkg/config/password_verifier_test.go`**

- Round-trip: save then verify returns true
- Wrong password: verify returns false, nil
- Missing file: `PasswordVerifierExists` returns false
- File permissions are 0600
- JSON structure contains version, salt, hash fields
- Atomic write: verify file is valid JSON even if process crashes (test rename semantics)

### Phase 2: Auth Middleware Update

**Modified: `pkg/webui/server.go` — `requireAuth()`**

Current flow:
```
1. Get password from GetWebUIPassword()
2. If empty -> 401
3. Compare Basic Auth credentials -> 401 or pass
```

New flow:
```
1. Get password from GetWebUIPassword()
2. If non-empty -> compare as today (fast path, unchanged)
3. If empty AND verifier file exists:
   a. Extract password from Basic Auth header
   b. Call tryRecoverPassword(password)
   c. If valid -> proceed to handler
   d. If invalid -> 401
4. If empty AND no verifier -> 401 (should not happen)
```

**New method on Server: `tryRecoverPassword(password string) bool`**

- Guarded with `sync.Once` — recovery runs at most once per process
- Calls `config.VerifyPassword(workDir, password)`
- On success: `config.SetProjectPassword(password)`
- On success + secrets file exists: `decryptAndLoadSecrets(workDir, password)` (extracted helper)
- Logs: "Password verified. Secrets decrypted." or "Password verified. No secrets file found."
- Returns true on success, false on verification failure

The `sync.Once` ensures the verify+decrypt path only runs once. After that, `GetWebUIPassword()` returns the cached value and all subsequent requests use the fast path. If the first login attempt fails (wrong password), the `sync.Once` is not consumed — we need a slightly different pattern: an `atomic.Bool` that flips to true on success, with a mutex protecting the verify+decrypt section to prevent concurrent recovery attempts.

```go
type Server struct {
    // ... existing fields ...
    passwordRecovered atomic.Bool
    recoverMu         sync.Mutex
}

func (s *Server) tryRecoverPassword(password string) bool {
    if s.passwordRecovered.Load() {
        return false // already recovered, shouldn't be here
    }
    s.recoverMu.Lock()
    defer s.recoverMu.Unlock()
    if s.passwordRecovered.Load() {
        return false // double-check after lock
    }

    ok, err := config.VerifyPassword(s.workDir, password)
    if err != nil || !ok {
        return false
    }

    config.SetProjectPassword(password)
    // Decrypt secrets if file exists
    if config.SecretsFileExists(s.workDir) {
        secrets, err := config.DecryptSecretsFile(s.workDir, password)
        if err != nil {
            s.logger.Error("Password verified but secrets decryption failed: %v", err)
            // Still mark as recovered — password is valid, secrets may be corrupt
        } else {
            config.SetDecryptedSecrets(secrets)
            s.logger.Info("Password recovered via WebUI login. Secrets decrypted.")
        }
    }

    s.passwordRecovered.Store(true)
    return true
}
```

### Phase 3: Startup Flow Changes

**Modified: `cmd/maestro/main.go` — `run()`**

Current (lines 105-115):
```go
ensureWebUIPassword()
if err := handleSecretsDecryption(projectDir); err != nil {
    // hard-fail
}
```

New:
```go
ensureWebUIPassword(projectDir)
handleSecretsDecryptionIfReady(projectDir)
```

**Modified: `cmd/maestro/flows.go` — `ensureWebUIPassword()`**

Now takes `projectDir` parameter. New logic:

```go
func ensureWebUIPassword(projectDir string) {
    // 1. MAESTRO_PASSWORD env var (highest precedence)
    if config.GetWebUIPassword() != "" {
        if config.GetProjectPassword() != "" {
            logger("Password loaded from project secrets")
        } else {
            config.SetProjectPassword(config.GetWebUIPassword())
            logger("Password loaded from MAESTRO_PASSWORD environment variable")
        }
        // Ensure verifier exists for future runs without env var
        if !config.PasswordVerifierExists(projectDir) {
            _ = config.SavePasswordVerifier(projectDir, config.GetProjectPassword())
        }
        return
    }

    // 2. Verifier file exists (password established in prior session)
    if config.PasswordVerifierExists(projectDir) {
        // Do NOT generate a new password
        // Password will be recovered via Basic Auth login to WebUI
        fmt.Println()
        fmt.Println("Maestro password required.")
        fmt.Println("Log in to the WebUI with your Maestro password to continue.")
        fmt.Println()
        fmt.Println("Lost your password? Delete .maestro/.password-verifier.json")
        fmt.Println("and .maestro/secrets.json.enc, then restart to generate a new one.")
        fmt.Println()
        return
    }

    // 3. Check for orphaned secrets file (migration case)
    if config.SecretsFileExists(projectDir) {
        // Secrets exist but no verifier — pre-verifier project
        // Hard-fail: we can't generate a password that would decrypt these
        fmt.Fprintf(os.Stderr,
            "Secrets file exists but no password verifier found.\n"+
            "Set MAESTRO_PASSWORD environment variable to decrypt.\n")
        os.Exit(1)
    }

    // 4. First run: generate password, create verifier
    password, err := generateSecurePassword(16)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to generate password: %v\n", err)
        os.Exit(1)
    }
    config.SetProjectPassword(password)
    if err := config.SavePasswordVerifier(projectDir, password); err != nil {
        fmt.Fprintf(os.Stderr, "Failed to save password verifier: %v\n", err)
        os.Exit(1)
    }

    // Display banner (updated)
    fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
    fmt.Println("║                   Project Password Generated                      ║")
    fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
    fmt.Printf( "║  Username: maestro                                                ║\n")
    fmt.Printf( "║  Password: %-52s ║\n", password)
    fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
    fmt.Println("║  RECORD THIS PASSWORD - it will not be shown again.               ║")
    fmt.Println("║  This password is used for WebUI login AND secrets encryption.    ║")
    fmt.Println("║  If lost, stored secrets cannot be recovered.                     ║")
    fmt.Println("║                                                                   ║")
    fmt.Println("║  Tip: Set MAESTRO_PASSWORD env var to use your own password.      ║")
    fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
    fmt.Println()
}
```

**Modified: `cmd/maestro/flows.go` — `handleSecretsDecryptionIfReady()`**

Replaces `handleSecretsDecryption()`. Only attempts decryption if password is already in memory:

```go
func handleSecretsDecryptionIfReady(projectDir string) {
    // If no password in memory, secrets will be decrypted lazily after WebUI login
    password := config.GetProjectPassword()
    if password == "" {
        if config.SecretsFileExists(projectDir) {
            config.LogInfo("Secrets file found. Will decrypt after password is provided via WebUI.")
        }
        return
    }

    // Password available — decrypt immediately (env var or first-run path)
    if !config.SecretsFileExists(projectDir) {
        return
    }

    secrets, err := config.DecryptSecretsFile(projectDir, password)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to decrypt secrets (check password): %v\n", err)
        os.Exit(1)
    }
    config.SetDecryptedSecrets(secrets)
    config.LogInfo("Secrets decrypted and loaded into memory")
}
```

### Phase 4: UI Warnings (Conditional)

Warnings only show when using a generated password (no `MAESTRO_PASSWORD` env var). Power users who set the env var don't need reminders.

**Modified: `pkg/webui/web/templates/setup.html`**

Add a warning banner above the "Required API Keys" card. The setup page is only shown when keys are missing, which is the exact scenario where users are about to add secrets:

```html
<div class="bg-amber-50 border border-amber-200 rounded-lg p-4 mb-6">
    <p class="text-sm text-amber-800 font-medium">Important: Record Your Password</p>
    <p class="text-sm text-amber-700 mt-1">
        Your WebUI login password is also used to encrypt secrets stored by Maestro.
        If you lose this password, stored secrets cannot be recovered.
        You can avoid this by setting the <code class="bg-amber-100 px-1 rounded">MAESTRO_PASSWORD</code>
        environment variable before starting Maestro.
    </p>
</div>
```

**Modified: `pkg/webui/web/templates/base.html` — Secrets Modal**

The existing `secrets-password-warning` div (line 98) already conditionally shows based on the API response `warning` field. We update the warning text on the backend (below) but keep the conditional display — it only shows when `MAESTRO_PASSWORD` is not set.

**Modified: `pkg/webui/secrets_handlers.go` — `handleSecretsList()`**

Update the warning message at line 57 to be more explicit about the risk:

```go
if os.Getenv("MAESTRO_PASSWORD") == "" {
    response["warning"] = "Secrets are encrypted with your WebUI login password. " +
        "If you lose this password, secrets cannot be recovered. " +
        "Set MAESTRO_PASSWORD env var to use a persistent password."
}
```

### Phase 5: README Update

**Modified: `README.md`**

In the Quickstart section, after the Step 4 setup mode note, add a warning about password persistence:

```markdown
> **Important:** When Maestro generates a password, it is used for both WebUI login
> and secrets encryption. Record it somewhere safe — if lost, any secrets stored
> through the WebUI cannot be recovered. To use your own persistent password, set
> the `MAESTRO_PASSWORD` environment variable before running Maestro.
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/config/password_verifier.go` | **New** — scrypt verifier: Save, Verify, Exists |
| `pkg/config/password_verifier_test.go` | **New** — unit tests |
| `pkg/webui/server.go` | Modify `requireAuth()` + add `tryRecoverPassword()` with atomic guard |
| `cmd/maestro/main.go` | Pass projectDir to `ensureWebUIPassword`; replace `handleSecretsDecryption` with `handleSecretsDecryptionIfReady` |
| `cmd/maestro/flows.go` | Rewrite `ensureWebUIPassword(projectDir)` with verifier logic; add `handleSecretsDecryptionIfReady()`; update banner text |
| `pkg/webui/secrets_handlers.go` | Update warning message text |
| `pkg/webui/web/templates/setup.html` | Add password warning banner |
| `pkg/webui/web/templates/base.html` | No change (existing conditional warning is sufficient) |
| `README.md` | Add password/secrets warning in Quickstart |

## Testing

**Unit tests (`pkg/config/password_verifier_test.go`):**
- Round-trip: save then verify returns true
- Wrong password: verify returns false, nil
- Missing file: `PasswordVerifierExists` returns false, `VerifyPassword` returns descriptive error
- File permissions are 0600 after atomic write
- JSON structure contains version=1, non-empty salt, non-empty hash
- Salt is unique per call (two saves produce different salts)

**Auth recovery tests (extend `pkg/webui/setup_test.go` or new file):**
- `tryRecoverPassword` with correct password: returns true, `GetProjectPassword()` is set
- `tryRecoverPassword` with wrong password: returns false, `GetProjectPassword()` remains empty
- `tryRecoverPassword` called twice with correct password: second call is no-op (atomic guard)
- `tryRecoverPassword` with correct password + secrets file: secrets are decrypted
- `tryRecoverPassword` with correct password + no secrets file: succeeds without error

**Integration test (manual):**
1. Fresh run, no env vars -> password generated, `.password-verifier.json` created, banner shown
2. Add secrets via setup page -> `secrets.json.enc` created
3. Restart without `MAESTRO_PASSWORD` -> WebUI starts, login with generated password -> secrets decrypted, agents start
4. Restart with wrong password -> 401, agents blocked
5. Restart with `MAESTRO_PASSWORD` set -> immediate startup
6. Delete `.password-verifier.json` + `secrets.json.enc` -> fresh start, new password
7. Legacy: `secrets.json.enc` exists, no verifier, no env var -> hard-fail with clear message

## Non-Goals

- Password reset/recovery flow (lose password = lose secrets, documented)
- Multiple password support or password rotation
- Changes to the encryption scheme of `secrets.json.enc`
- Automatic migration of pre-verifier projects (they must use `MAESTRO_PASSWORD`)
