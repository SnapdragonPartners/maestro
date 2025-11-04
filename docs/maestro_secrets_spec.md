# Maestro Secrets Management — Functional Specification

**Version:** 2.0
**Status:** Ready for Implementation
**Last Updated:** 2025-01-04

---

## 1. Purpose & Scope

### 1.1 Goal

**Make Maestro usable for people who don't know what environment variables are or how to set them.**

This feature provides an optional, user-friendly "remember my credentials" mechanism for local development. It is NOT a production-grade secret management system.

### 1.2 Core Principles

- **Optional:** Encrypted secrets are completely optional. Environment variables remain the primary method for technical users.
- **Simple:** No complex workflows, no team coordination, no key rotation.
- **Local-only:** Single-user, single-machine. Not designed for shared credentials or version control.
- **Safe fallback:** System always falls back to environment variables if secrets file is unavailable.

### 1.3 Out of Scope

These features are explicitly NOT included:

- ❌ Multi-user / team credential sharing
- ❌ Secret rotation workflows
- ❌ `maestro secrets init` / `edit` / `show` commands (MVP only creates during bootstrap)
- ❌ Integration with external secret managers (Vault, AWS Secrets Manager, etc.)
- ❌ Audit logging of secret access
- ❌ Migration tools (pre-release product)
- ❌ OS keychain integration

---

## 2. Architecture

### 2.1 Directory Structure

```
<projectDir>/.maestro/
  ├── config.json          # non-sensitive configuration
  ├── maestro.db           # database
  ├── secrets.json.enc     # encrypted secrets (optional, created during bootstrap)
  ├── server.crt           # SSL certificate (optional, copied during bootstrap)
  └── server.key           # SSL private key (optional, copied during bootstrap)
```

**Important:** The entire `.maestro/` directory is excluded from version control.

### 2.2 Supported Secrets

| Secret Name | Purpose | Required When |
|-------------|---------|---------------|
| `GITHUB_TOKEN` | Git operations (clone, push, PR creation) | Always |
| `ANTHROPIC_API_KEY` | Claude models | Using Anthropic models |
| `OPENAI_API_KEY` | OpenAI models (o3, GPT, etc.) | Using OpenAI models |
| `SSL_KEY_PEM` | WebUI HTTPS private key | WebUI SSL enabled |

**Note:** SSL certificates are public data and stored unencrypted as `.maestro/server.crt`.

### 2.3 Storage Format

**File:** `.maestro/secrets.json.enc`
**Permissions:** `0600` (owner read/write only)
**Format:** Binary file with structure:

```
[16-byte salt][12-byte nonce][AES-GCM ciphertext + auth tag]
```

**Decrypted JSON content:**
```json
{
  "GITHUB_TOKEN": "ghp_abc123...",
  "ANTHROPIC_API_KEY": "sk-ant-xyz...",
  "OPENAI_API_KEY": "sk-proj-...",
  "SSL_KEY_PEM": "-----BEGIN PRIVATE KEY-----\nMIIE..."
}
```

**Values:** Literal credential values (not file paths). PEM content stored as-is (Go JSON marshalling handles control characters).

### 2.4 Encryption Scheme

- **Algorithm:** AES-256-GCM (authenticated encryption)
- **Key Derivation:** `scrypt.Key(password, salt, N=32768, r=8, p=1, keyLen=32)`
- **Salt:** 16 random bytes (generated per encryption, stored in file)
- **Nonce:** 12 random bytes (generated per encryption, stored in file)
- **Randomness:** `crypto/rand` for all random values

**Security properties:**
- Authenticated encryption prevents tampering
- Unique salt per file prevents rainbow table attacks
- scrypt protects against brute-force password attacks

---

## 3. Runtime Behavior

### 3.1 Unified Precedence Model

All secrets use the same resolution order (highest to lowest priority):

1. **`config.json` paths** (SSL only)
   - If `webui.cert` or `webui.key` paths exist, load from those files
   - Paths are relative to `<projectDir>`

2. **Encrypted secrets file** (`.maestro/secrets.json.enc`)
   - If present and decryptable, extract values

3. **Environment variables**
   - Standard fallback: `GITHUB_TOKEN`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`
   - SSL: `MAESTRO_SSL_CERT` and `MAESTRO_SSL_KEY` (if needed)

### 3.2 Password Handling

**Interactive mode:**
- Prompt user: `Enter project password: ******`
- Use `golang.org/x/term.ReadPassword()` for hidden input
- Clear password from memory immediately after use

**Non-interactive mode:**
- Check for `MAESTRO_PASSWORD` environment variable
- If present, use it (no prompt)
- Enables CI/CD and automation

**Memory safety:**
```go
defer func() {
    for i := range passwordBytes {
        passwordBytes[i] = 0
    }
}()
```

### 3.3 API Functions

**New file:** `pkg/config/secrets.go`

```go
// GetSecret returns a secret value by name using standard precedence.
// Falls back through: config paths → secrets file → environment variables.
func GetSecret(name string) (string, error)

// GetSSLCertAndKey returns SSL certificate and key bytes.
// Follows precedence: config paths → secrets file → env vars.
func GetSSLCertAndKey(cfg *Config) (certPEM []byte, keyPEM []byte, error)

// EncryptSecretsFile encrypts and saves secrets to .maestro/secrets.json.enc.
// Sets file permissions to 0600.
func EncryptSecretsFile(projectDir string, password string, secrets map[string]string) error

// DecryptSecretsFile decrypts and returns secrets from .maestro/secrets.json.enc.
func DecryptSecretsFile(projectDir string, password string) (map[string]string, error)

// SecretsFileExists checks if secrets.json.enc exists in project directory.
func SecretsFileExists(projectDir string) bool
```

**Integration points:**
- Update `GetAPIKey()` (config.go:1459) to call `GetSecret()`
- Update `GetGitHubToken()` (config.go:1512) to call `GetSecret()`
- Update WebUI SSL loading to use `GetSSLCertAndKey()`

---

## 4. Bootstrap Flow

### 4.1 Interactive Prompt

After platform detection, show:

```
🔐 Credential Storage

By default, Maestro reads your credentials for services like GitHub, Anthropic,
and OpenAI from environment variables.

If you don't know what this means or want to store credentials securely in this
project, Maestro can encrypt and save them for you.

Would you like to store credentials in Maestro? [y/N]:
```

**If user answers `N` or presses Enter:**
- Skip credential prompts
- System uses environment variables only
- Continue with rest of bootstrap

**If user answers `y`:**
- Proceed to credential entry (section 4.2)

### 4.2 Credential Entry

**Step 1: Set encryption password**
```
Enter a password to encrypt your credentials: ******
Confirm password: ******
```

**Step 2: Collect required secrets**
```
Enter GITHUB_TOKEN (required): ghp_xxx
```

**Step 3: Collect optional API keys**
```
Enter ANTHROPIC_API_KEY (optional, press Enter to skip): sk-ant-xxx
Enter OPENAI_API_KEY (optional, press Enter to skip): sk-xxx
```

**Step 4: SSL configuration** (only if WebUI enabled with SSL)
```
🔒 SSL Certificate Setup

Maestro will copy your SSL certificate and private key to .maestro/
for portability and security.

Path to SSL certificate (PEM format): /path/to/cert.pem
Path to SSL private key (PEM format): /path/to/key.pem

✅ Copied certificate to .maestro/server.crt
✅ Encrypted private key saved to secrets.json.enc
```

**Step 5: Save**
```
🔐 Encrypting and saving credentials...
✅ Credentials saved to .maestro/secrets.json.enc (file permissions: 0600)
```

### 4.3 SSL File Handling

**Decision:** Option A (always copy to `.maestro/`)

1. Prompt user for cert and key file paths
2. Copy certificate → `.maestro/server.crt` (unencrypted, world-readable)
3. Read private key, store in `secrets.json.enc` as `SSL_KEY_PEM` (encrypted)
4. Update `config.json` with paths:
   ```json
   {
     "webui": {
       "ssl": true,
       "cert": ".maestro/server.crt",
       "key": ".maestro/server.key"
     }
   }
   ```

**Note:** `server.key` path is a placeholder in config. Actual key content is in encrypted file.

---

## 5. Startup & Error Handling

### 5.1 Normal Startup

**If `.maestro/secrets.json.enc` exists:**

1. Check for `MAESTRO_PASSWORD` environment variable
2. If not found, prompt: `Enter project password: ******`
3. Attempt decryption
4. On failure → section 5.2 (decryption failure)
5. On success → load secrets into memory (used via `GetSecret()`)

**If secrets file does NOT exist:**

1. Attempt to load all credentials from environment variables
2. If required credentials missing → section 5.3 (missing credentials error)

### 5.2 Decryption Failure

```
⚠️  Unable to decrypt secrets file with specified password.

Do you want to (R)etry or (D)elete the secrets file and restart? [R/d]:
```

**If Retry (default):**
- Allow up to 3 total attempts
- After 3 failures, treat as if user selected "Delete"

**If Delete:**
```
⚠️  Deleting .maestro/secrets.json.enc...
✅ Secrets file removed. Attempting to continue with environment variables...
```
- Remove `.maestro/secrets.json.enc`
- Attempt to load from environment variables
- If still missing required credentials → section 5.3

### 5.3 Missing Credentials Error

```
❌ Critical credentials for required services were not found in config
   files or environment variables.

You can either:
  1. Set environment variables:
     export GITHUB_TOKEN=ghp_xxx
     export ANTHROPIC_API_KEY=sk-ant-xxx

  2. Run bootstrap mode to configure credentials:
     maestro -bootstrap

  3. Learn more about credential setup:
     https://docs.maestro.dev/secrets

Maestro cannot start without required credentials.
```

Then exit with status code 1.

---

## 6. Implementation Checklist

### 6.1 New Files

- [ ] `pkg/config/secrets.go` - All encryption/decryption logic
  - [ ] `EncryptSecretsFile()`
  - [ ] `DecryptSecretsFile()`
  - [ ] `GetSecret()`
  - [ ] `GetSSLCertAndKey()`
  - [ ] `SecretsFileExists()`

### 6.2 Modified Files

- [ ] `pkg/config/config.go`
  - [ ] Update `GetAPIKey()` to check secrets file first
  - [ ] Update `GetGitHubToken()` to check secrets file first
  - [ ] Add secrets file validation in `validateConfig()`

- [ ] `cmd/maestro/interactive_bootstrap.go`
  - [ ] Add credential storage prompt (section 4.1)
  - [ ] Add credential entry flow (section 4.2)
  - [ ] Add SSL file copying logic (section 4.3)

- [ ] `pkg/webui/server.go`
  - [ ] Update `StartServer()` to use `GetSSLCertAndKey()`

### 6.3 Security Checklist

- [ ] Set file permissions to `0600` on creation
- [ ] Verify permissions before reading
- [ ] Use `crypto/rand` for all random generation
- [ ] Zero password bytes after use
- [ ] Use `golang.org/x/term.ReadPassword()` for password input
- [ ] Never log passwords (not even length)
- [ ] Validate salt and nonce lengths on decryption
- [ ] Handle corrupted files gracefully (don't crash)

### 6.4 Test Coverage

- [ ] Encryption/decryption round-trip
- [ ] Wrong password (1, 2, 3 attempts)
- [ ] Corrupted file (truncated, wrong format)
- [ ] Missing file fallback to env vars
- [ ] Precedence validation (config → secrets → env)
- [ ] Non-interactive mode (`MAESTRO_PASSWORD` env var)
- [ ] File permissions enforcement
- [ ] Memory zeroing (password cleared after use)
- [ ] SSL cert/key loading from all sources

### 6.5 Documentation Updates

- [ ] Add secrets management section to main docs
- [ ] Update bootstrap documentation
- [ ] Add FAQ: "When should I use encrypted secrets vs env vars?"
- [ ] Document `MAESTRO_PASSWORD` for CI/CD
- [ ] Add troubleshooting guide for decryption failures
- [ ] Update `.gitignore` examples to include `.maestro/`

---

## 7. Example User Flows

### 7.1 New User (No Env Vars)

```bash
$ maestro -bootstrap
🚀 Maestro Bootstrap Setup
...
🔐 Credential Storage

By default, Maestro reads your credentials from environment variables.
Would you like to store credentials in Maestro? [y/N]: y

Enter a password to encrypt your credentials: ******
Confirm password: ******

Enter GITHUB_TOKEN (required): ghp_abc123...
Enter ANTHROPIC_API_KEY (optional): sk-ant-xyz...
Enter OPENAI_API_KEY (optional): [Enter]

🔐 Encrypting and saving credentials...
✅ Credentials saved to .maestro/secrets.json.enc
✅ Bootstrap complete!
```

### 7.2 Technical User (Env Vars Set)

```bash
$ export GITHUB_TOKEN=ghp_abc123
$ export ANTHROPIC_API_KEY=sk-ant-xyz
$ maestro -bootstrap
🚀 Maestro Bootstrap Setup
...
🔐 Credential Storage

By default, Maestro reads your credentials from environment variables.
Would you like to store credentials in Maestro? [y/N]: [Enter]

✅ Using environment variables for credentials
✅ Bootstrap complete!
```

### 7.3 Subsequent Startup

```bash
$ maestro
📋 Loading project from /path/to/project
Enter project password: ******
✅ Credentials decrypted successfully
🚀 Starting Maestro...
```

### 7.4 CI/CD Automation

```bash
$ export MAESTRO_PASSWORD=my-secret-pass
$ maestro
📋 Loading project from /path/to/project
✅ Credentials decrypted successfully (MAESTRO_PASSWORD)
🚀 Starting Maestro...
```

### 7.5 Wrong Password Recovery

```bash
$ maestro
📋 Loading project from /path/to/project
Enter project password: ******
⚠️  Unable to decrypt secrets file with specified password.

Do you want to (R)etry or (D)elete the secrets file and restart? [R/d]: r
Enter project password: ******
✅ Credentials decrypted successfully
🚀 Starting Maestro...
```

---

## 8. Error Message Reference

### 8.1 Decryption Failures

**Wrong password:**
```
⚠️  Unable to decrypt secrets file with specified password.

Do you want to (R)etry or (D)elete the secrets file and restart? [R/d]:
```

**Corrupted file:**
```
⚠️  Secrets file appears to be corrupted or invalid format.

Do you want to (D)elete the secrets file and restart? [y/N]:
```

**File permission error:**
```
⚠️  Secrets file has incorrect permissions (found: 0644, expected: 0600).
   This is a security risk. Maestro will fix this automatically.

✅ File permissions corrected to 0600
```

### 8.2 Missing Credentials

**No credentials found:**
```
❌ Critical credentials for required services were not found in config
   files or environment variables.

You can either:
  1. Set environment variables:
     export GITHUB_TOKEN=ghp_xxx
     export ANTHROPIC_API_KEY=sk-ant-xxx

  2. Run bootstrap mode to configure credentials:
     maestro -bootstrap

  3. Learn more about credential setup:
     https://docs.maestro.dev/secrets

Maestro cannot start without required credentials.
```

### 8.3 Bootstrap Errors

**Password mismatch:**
```
❌ Passwords do not match. Please try again.

Enter a password to encrypt your credentials: ******
Confirm password: ******
```

**SSL file not found:**
```
❌ SSL certificate file not found: /path/to/cert.pem

Please check the path and try again.
Path to SSL certificate (PEM format):
```

---

## 9. Technical Notes

### 9.1 Cryptographic Implementation

**Dependencies:**
- `crypto/aes` - AES cipher
- `crypto/cipher` - GCM mode
- `crypto/rand` - Secure random generation
- `golang.org/x/crypto/scrypt` - Key derivation

**Key derivation parameters:**
- `N=32768` (2^15) - CPU/memory cost parameter
- `r=8` - Block size
- `p=1` - Parallelization
- `keyLen=32` - Output key length (256 bits)

**Rationale:** These parameters provide strong protection against brute-force attacks while remaining fast enough for interactive use (~100ms on modern hardware).

### 9.2 File Format Details

```
Offset  Length  Content
------  ------  -------
0       16      Salt (random, used for scrypt)
16      12      Nonce (random, used for AES-GCM)
28      N       Ciphertext + 16-byte GCM authentication tag
```

**Total file size:** 28 + ciphertext_length + 16 bytes

### 9.3 Error Handling Philosophy

**Always provide:**
1. **What happened** - Clear, non-technical explanation
2. **What to do** - Actionable next steps with examples
3. **Where to learn more** - Documentation link

**Avoid:**
- Technical jargon ("scrypt key derivation failed")
- Vague errors ("Error loading secrets")
- Dead-ends ("Invalid password." → no guidance on what to do)

---

## 10. Future Enhancements

These features may be added post-MVP based on user feedback:

**Convenience commands:**
- `maestro secrets init` - Create secrets file from current env vars
- `maestro secrets edit` - Interactive credential editor
- `maestro secrets show` - Display masked credential status
- `maestro secrets rotate` - Update a single credential

**Advanced features:**
- Integration hooks for external secret managers (Vault, AWS Secrets Manager)
- Audit logging of secret access
- Secret expiration warnings
- Password strength requirements
- Automatic backup before password change

**Multi-user support:**
- Per-user encrypted secrets
- Shared project secrets with team passwords
- Role-based access control

---

## Success Criteria

This feature is successful if:

1. ✅ A user who has never set an environment variable can complete Maestro bootstrap
2. ✅ A technical user with env vars set experiences no interruption or confusion
3. ✅ CI/CD pipelines work seamlessly via `MAESTRO_PASSWORD` env var
4. ✅ Wrong password scenarios don't cause crash loops or data loss
5. ✅ Missing credentials provide clear, actionable error messages
6. ✅ Documentation clearly explains when to use each approach
7. ✅ No secrets are ever stored in plaintext on disk
8. ✅ System gracefully degrades to env vars when secrets file unavailable

---

## Revision History

- **v2.0** (2025-01-04) - Simplified based on architecture review
  - Clarified goal as UX feature for non-technical users
  - Made feature completely optional in bootstrap
  - Unified precedence model for all secrets
  - Simplified SSL handling (only encrypt private keys)
  - Added explicit error handling with retry/delete flows
  - Removed multi-user and migration features (out of scope)
  - Added comprehensive implementation checklist

- **v1.0** (Initial draft) - Original specification
