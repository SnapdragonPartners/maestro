# Required Changes to Maestro Go CLI

Changes needed to support the macOS menu bar app wrapper. These are the only modifications required to the core Maestro codebase.

---

## 1. Session Token Auth Endpoint

### Problem

The macOS app manages the password via Keychain and injects it into the Go binary via `MAESTRO_PASSWORD`. However, when the user clicks "Open Web UI", the app opens Safari to `localhost:<port>`. Safari receives a Basic Auth challenge and shows a username/password dialog — but the user never sees the password (by design). Safari also strips credentials from URLs (`http://user:pass@host`), so there's no way to pass them through the browser.

### Solution

Add an alternative auth path: a session token endpoint that sets a cookie.

### Implementation

**1. Read `MAESTRO_SESSION_TOKEN` env var at startup**

The macOS app generates a random token per launch and passes it via environment alongside `MAESTRO_PASSWORD`.

**2. Add route: `GET /auth/session?token=<token>`**

Handler logic:
- Compare `token` query param against stored `MAESTRO_SESSION_TOKEN` value
- If valid: set a session cookie (e.g., `maestro_session=<signed-value>`, `HttpOnly`, `SameSite=Strict`, `Path=/`)
- Redirect to `/`
- Invalidate the token after first use (optional but recommended for MVP)

**3. Update Basic Auth middleware to accept session cookie**

In the existing auth middleware that checks Basic Auth credentials:
- Before checking Basic Auth, check for a valid `maestro_session` cookie
- If the cookie is present and valid, allow the request through
- If no valid cookie, fall back to standard Basic Auth check

### What Stays the Same

- Basic Auth works exactly as before for CLI users, API clients, and any non-browser access
- `MAESTRO_PASSWORD` behavior is unchanged
- Password generation, verifier files, and all existing auth logic remain untouched

### Env Vars

| Variable | Set By | Purpose |
|----------|--------|---------|
| `MAESTRO_PASSWORD` | macOS app (or user) | Existing — password for Basic Auth |
| `MAESTRO_SESSION_TOKEN` | macOS app | New — one-time token for browser auth |

### Example Launch

```bash
MAESTRO_PASSWORD=abc123 MAESTRO_SESSION_TOKEN=randomxyz \
  maestro -projectdir /Users/user/my-project
```

macOS app then opens: `http://localhost:8080/auth/session?token=randomxyz`

### Scope Estimate

- One new HTTP handler (~30 lines)
- One check added to existing auth middleware (~10 lines)
- One env var read at startup (~3 lines)
- No new dependencies
- No database changes
- No config file changes

---

## That's It

This is the only change required to the Go CLI for the macOS app MVP. Everything else (password management, process lifecycle, Docker checks, port detection) is handled entirely by the macOS wrapper.
