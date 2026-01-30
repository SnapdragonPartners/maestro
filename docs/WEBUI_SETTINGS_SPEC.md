# WebUI Settings Page Specification

## Overview

This feature adds a dedicated Settings page to the WebUI that allows users to configure sensitive credentials (API keys, tokens) through the browser interface rather than requiring environment variables. This targets users who may not be comfortable with command-line configuration.

## Motivation

Currently, all sensitive configuration (API keys, GitHub tokens, etc.) must be set via environment variables. This creates a barrier for non-technical users who want to use Maestro. The encrypted secrets system already exists (`pkg/config/secrets.go`) with full AES-256-GCM encryption, but there's no UI to manage these secrets.

## Goals

1. Provide a user-friendly interface for managing all configurable credentials
2. Show appropriate security warnings when SSL is not enabled
3. Ensure all credential access goes through the existing `GetSecret()` helper (encrypted store → env var fallback)
4. Changes take effect immediately (except SSL certificates which require restart)

## Non-Goals

- Changing non-sensitive configuration (rate limits, ports, etc.) - future work
- OAuth flows for GitHub/API authentication - use tokens directly
- Certificate generation or Let's Encrypt integration

## Key Architectural Decision

**This feature is a UI layer over the existing secrets system.** All configurable values use the existing `/api/secrets` endpoints and `GetSecret()` accessor. No new persistence mechanism is introduced. The settings page simply provides a user-friendly interface to the already-implemented encrypted secrets infrastructure.

## Design

### New Route: `/settings`

A dedicated settings page accessible via link from the main dashboard. The page will be a separate HTML template with its own JavaScript, following the existing WebUI patterns.

### Navigation

- Add a "Settings" link/icon to the dashboard header
- Settings page has a "Back to Dashboard" link

### SSL Warning Banner

When `cfg.WebUI.SSL` is `false`, display a prominent warning banner at the top of the settings page:

```
⚠️ Warning: SSL is not enabled. Credentials entered on this page will be transmitted
in plain text. Consider enabling SSL in your configuration for production use.
```

The warning should be dismissible but re-appear on page reload.

### Configurable Secrets

All environment variables currently used for sensitive configuration:

| Secret Name | Description | Type | Effect |
|-------------|-------------|------|--------|
| `ANTHROPIC_API_KEY` | Anthropic Claude API key | Password input | Immediate |
| `OPENAI_API_KEY` | OpenAI API key | Password input | Immediate |
| `GOOGLE_GENAI_API_KEY` | Google Gemini API key | Password input | Immediate |
| `GITHUB_TOKEN` | GitHub personal access token | Password input | Immediate |
| `GOOGLE_SEARCH_API_KEY` | Google Custom Search API key | Password input | Immediate |
| `GOOGLE_SEARCH_CX` | Google Custom Search Engine ID | Text input | Immediate |
| `OLLAMA_HOST` | Local Ollama server URL | Text input | Immediate |
| `MAESTRO_SSL_CERT` | SSL certificate (PEM format) | Textarea | Requires restart |
| `MAESTRO_SSL_KEY` | SSL private key (PEM format) | Textarea | Requires restart |

### UI Layout

```
┌─────────────────────────────────────────────────────────────────┐
│  ← Back to Dashboard                              Maestro Settings │
├─────────────────────────────────────────────────────────────────┤
│ ⚠️ Warning: SSL is not enabled...                          [✕] │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  LLM API Keys                                                   │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Anthropic API Key                          [Set ✓]      │   │
│  │ [••••••••••••••••••••••••]  [Save] [Clear]              │   │
│  │                                                          │   │
│  │ OpenAI API Key                             [Not Set]    │   │
│  │ [                        ]  [Save] [Clear]              │   │
│  │                                                          │   │
│  │ Google Gemini API Key                      [Not Set]    │   │
│  │ [                        ]  [Save] [Clear]              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  Git & GitHub                                                   │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ GitHub Token                               [Set ✓]      │   │
│  │ [••••••••••••••••••••••••]  [Save] [Clear]              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  Web Search                                                     │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Google Search API Key                      [Not Set]    │   │
│  │ [                        ]  [Save] [Clear]              │   │
│  │                                                          │   │
│  │ Google Search Engine ID (CX)               [Not Set]    │   │
│  │ [                        ]  [Save] [Clear]              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  Local Inference                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Ollama Host URL                            [Not Set]    │   │
│  │ [http://localhost:11434  ]  [Save] [Clear]              │   │
│  │ Default: http://localhost:11434                         │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  SSL/TLS Certificates  ⚠️ Changes require restart              │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ SSL Certificate (PEM)                      [Not Set]    │   │
│  │ ┌──────────────────────────────────────────────────┐    │   │
│  │ │                                                  │    │   │
│  │ │  Paste certificate PEM content here...          │    │   │
│  │ │                                                  │    │   │
│  │ └──────────────────────────────────────────────────┘    │   │
│  │                                        [Save] [Clear]   │   │
│  │                                                          │   │
│  │ SSL Private Key (PEM)                      [Not Set]    │   │
│  │ ┌──────────────────────────────────────────────────┐    │   │
│  │ │                                                  │    │   │
│  │ │  Paste private key PEM content here...          │    │   │
│  │ │                                                  │    │   │
│  │ └──────────────────────────────────────────────────┘    │   │
│  │                                        [Save] [Clear]   │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Status Indicators

Each secret shows its current status:
- **Set ✓** (green) - Value exists in encrypted store or environment
- **Not Set** (gray) - No value configured

The UI never displays actual secret values. Password inputs show masked characters if a value exists.

### API Endpoints

**All storage uses the existing `/api/secrets` infrastructure.** No new persistence endpoints are added.

#### `GET /api/secrets` (Enhanced)

**Existing**: Returns list of secret names that are set.

**Enhancement**: Return metadata for all configurable secrets (not just those currently set), plus SSL status. This enables the UI to show "set/unset" indicators and appropriate warnings.

New response format:
```json
{
  "secrets": [
    {
      "name": "ANTHROPIC_API_KEY",
      "description": "Anthropic Claude API key",
      "category": "llm",
      "is_set": true,
      "source": "encrypted",
      "requires_restart": false
    },
    {
      "name": "GITHUB_TOKEN",
      "description": "GitHub personal access token",
      "category": "git",
      "is_set": true,
      "source": "environment",
      "requires_restart": false
    },
    {
      "name": "MAESTRO_SSL_CERT",
      "description": "SSL certificate (PEM format)",
      "category": "ssl",
      "is_set": false,
      "source": "none",
      "requires_restart": true
    }
  ],
  "ssl_enabled": false,
  "ssl_effective": {
    "has_cert": false,
    "has_key": false
  }
}
```

Field definitions:
- `source`: `"encrypted"` (in secrets file), `"environment"` (from env var), or `"none"` (not configured)
- `ssl_enabled`: Current value of `cfg.WebUI.SSL`
- `ssl_effective`: Whether effective cert/key are available (uses existing `GetSSLCertAndKey()` logic to determine what would actually be used at runtime)

#### `POST /api/secrets` (Existing - No Changes)

Sets a secret value. Existing implementation stores in memory and persists to encrypted file.

**Request size limit**: Add 64KB limit for request body to prevent oversized PEM uploads. Typical cert chains are under 10KB.

```json
{
  "name": "ANTHROPIC_API_KEY",
  "value": "sk-ant-..."
}
```

#### `DELETE /api/secrets/:name` (Existing - No Changes)

Removes a secret from the encrypted store. If an environment variable exists with the same name, `GetSecret()` will fall back to it.

### Unified Secret Access

**Use the existing `GetSecret()` function** in `pkg/config/secrets.go`. This function already implements the correct precedence (encrypted secrets → environment variable fallback). No new helper function needed.

**Required changes:**

1. **Extend `GetSecret()` to support all configurable names** - Currently it may only handle a subset. Ensure these names work:
   - `GOOGLE_SEARCH_API_KEY`
   - `GOOGLE_SEARCH_CX`
   - `OLLAMA_HOST`
   - `MAESTRO_SSL_CERT`
   - `MAESTRO_SSL_KEY`

2. **Update direct `os.Getenv()` calls** to use `GetSecret()`:

| File | Current Code | Secret |
|------|--------------|--------|
| `pkg/preflight/checks.go:42` | `os.Getenv("GITHUB_TOKEN")` | GITHUB_TOKEN |
| `pkg/forge/factory.go:43` | `os.Getenv("GITHUB_TOKEN")` | GITHUB_TOKEN |
| `pkg/config/search.go:41` | `os.Getenv(EnvGoogleSearchAPIKey)` | GOOGLE_SEARCH_API_KEY |
| `pkg/config/search.go:42` | `os.Getenv(EnvGoogleSearchCX)` | GOOGLE_SEARCH_CX |
| `pkg/preflight/checks.go:179` | `os.Getenv("OLLAMA_HOST")` | OLLAMA_HOST |

This ensures that values set via the WebUI settings page take effect immediately throughout the application.

## Implementation Phases

### Phase 1: Backend Enhancements
1. Extend `GetSecret()` to support all configurable names (GOOGLE_SEARCH_*, OLLAMA_HOST, SSL vars)
2. Update direct `os.Getenv()` calls to use `GetSecret()` (5 files identified above)
3. **Audit all `os.Getenv` calls**: Run `grep -r "os\.Getenv" --include="*.go"` to find any missed sensitive config access. Document any intentional exceptions (e.g., PATH, HOME, debug flags that should remain env-only).
4. Enhance `GET /api/secrets` to return full secret metadata and SSL status
5. Add 64KB request body limit to `POST /api/secrets`

### Phase 2: Settings Page UI
1. Create `settings.html` template
2. Create `settings.js` for page logic
3. Add route handler for `/settings`
4. Add navigation link from dashboard header

### Phase 3: Polish & Testing
1. Add SSL warning banner with dismiss functionality
2. Add status indicators (set/unset, source) and save/clear feedback
3. Test all secret types (save, clear, immediate effect)
4. Test SSL cert fields with restart messaging
5. Verify CORS headers and Content-Type validation

## Security Considerations

1. **SSL Warning**: Prominent warning when SSL is disabled - credentials transmitted in plain text
2. **No Value Display**: Never return or display actual secret values via API or UI (not even partially masked)
3. **Authentication**: All endpoints protected by existing Basic Auth middleware
4. **Encryption at Rest**: Secrets stored in AES-256-GCM encrypted file with 0600 permissions
5. **Memory Safety**: Existing password zeroing and mutex protection maintained
6. **Request Size Limits**: 64KB limit on POST body to prevent oversized uploads
7. **CSRF Mitigations**:
   - Basic Auth over HTTPS provides baseline protection (credentials not automatically sent by cross-origin requests without explicit header)
   - Set conservative CORS headers (no `Access-Control-Allow-Origin: *`)
   - Verify `Content-Type: application/json` on POST requests (browsers won't send JSON cross-origin without preflight)
8. **Logging**: Never log secret values, even in debug mode

## Testing Strategy

### Unit Tests
- `GET /api/secrets` returns correct metadata
- `GetSecret()` fallback behavior (encrypted → env var)
- Secret status detection (encrypted vs env var vs none)

### Integration Tests
- Save secret via UI → verify immediate effect in subsequent API calls
- Clear secret → verify fallback to env var (if set)
- SSL cert save → verify restart message displayed

### Manual Testing
- Full flow: open settings, add API key, verify agent can use it
- SSL warning appears when SSL disabled
- Password inputs properly masked
- Status indicators update after save/clear

## Open Questions

None - all questions resolved in pre-spec discussion and external review.

## References

- Existing secrets implementation: `pkg/config/secrets.go` (includes `GetSecret()`, `GetSSLCertAndKey()`)
- Existing secrets API handlers: `pkg/webui/secrets_handlers.go`
- Secrets specification: `docs/maestro_secrets_spec.md`
- WebUI architecture: `pkg/webui/server.go`, `pkg/webui/web/`
- Route registration: `pkg/webui/server.go:RegisterRoutes()`
