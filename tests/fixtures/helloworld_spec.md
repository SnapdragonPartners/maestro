
# Helloworld Go Web Server Specification

## Overview

This project implements a minimal HTTP server in Go 1.24.3 with two endpoints:

- `/health`: A health check endpoint returning a simple "OK"
- `/`: A homepage rendered from a Go HTML template (`home.html`)

The structure is flat, using only standard library packages, with support for build, test, and run via a `Makefile`. Functional tests will verify the endpoints.

---

## Project Structure

```
helloworld/
├── go.mod
├── main.go
├── home.html
├── main_test.go
├── Makefile
```

---

## Endpoints

### `/health`
- **Method:** `GET`
- **Response Code:** `200 OK`
- **Response Body:** `"OK"`
- **Content-Type:** `text/plain`

### `/`
- **Method:** `GET`
- **Response Code:** `200 OK`
- **Response Body:** HTML rendered from `home.html`
- **Content-Type:** `text/html`
- **Template File:** `home.html` (static content)

---

## Build and Run

### Makefile Targets

```make
build:
	go fmt ./...
	go build -o helloworld .

run:
	./helloworld

test:
	go test ./...
```

---

## Testing

- Use Go’s built-in `testing` and `net/http/httptest`.
- Functional tests will:
  - Start a test server
  - Make HTTP requests to `/` and `/health`
  - Assert status codes and content

---

## Code Guidelines

- Use `log.Println` for server-side output (avoid `fmt.Println`)
- No logging middleware or structured logging for now
- Hardcoded configuration
- Keep code modular and clean, but no need for extra layers

---

## Out of Scope

- Dockerfile or containerization
- Logging framework
- Environment-based config
- CI/CD or GitHub Actions
- Extensibility scaffolding

---

## Go Version

Pin to Go version **1.24.3**
