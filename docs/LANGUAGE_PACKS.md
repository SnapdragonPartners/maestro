# Language Packs

This document tracks the implementation of platform/language packs for Maestro bootstrap templates.

## Overview

Language packs provide platform-specific configuration for bootstrap templates. They enable a single consolidated template to work across multiple platforms (Go, Node.js, Python, etc.) while maintaining platform-specific tooling, commands, and best practices.

## Problem Statement

The original implementation had separate templates for each platform (`bootstrap.tpl.md` for generic, `golang.tpl.md` for Go). This led to:

1. **Duplication** - Common sections (container requirements, git access, validation) repeated across templates
2. **Inconsistency** - Fixes in one template not propagated to others
3. **Empty placeholders** - Generic template rendered `{{.BuildCommand}}` as empty when not populated
4. **Hardcoded assumptions** - Claude Code references, nonsensical items like "Check database connectivity"
5. **Difficult extensibility** - Adding a new platform required creating an entire new template

## Design

### Token Replacement Contract

Packs can contain tokens that are replaced at render time. This is a simple, deterministic substitution - not Go templates.

**Allowed Tokens:**
| Token | Source | Required |
|-------|--------|----------|
| `${PROJECT_NAME}` | `TemplateData.ProjectName` | Yes (always available) |
| `${LANGUAGE_VERSION}` | `TemplateData.LanguageVersion` or `Pack.LanguageVersion` | No |

**Replacement Rules:**
1. Single pass, exact-match replacement (no nesting, no functions, no conditionals)
2. After replacement, assert **no `${` remains** - this catches typos and unknown tokens
3. If a pack uses `${LANGUAGE_VERSION}` but the value is empty → render error → fallback to generic

**Validation:**
- Loader validates token usage: regex `\$\{[A-Z0-9_]+\}` ensures every match is in the allowlist
- Unknown tokens (e.g., `${LANG_VER}`) cause validation failure with clear error message

### Responsibility Split

**Pack Loader (`packs.go`):**
- Reads JSON from embedded files
- Validates schema + required fields
- Validates token usage (all tokens in allowlist)
- Returns `Pack` struct "as-authored" (no substitutions)

**Renderer (`renderer.go`):**
- Receives `Pack` + `TemplateData`
- Calls `pack.Rendered(templateData)` to get substituted copy
- Injects resulting strings into `bootstrap.tpl.md`
- Asserts no unrendered tokens remain

### Architecture

```
pkg/templates/
├── packs/
│   ├── packs.go              # Pack loader, validator, registry
│   ├── packs_test.go         # Pack validation tests
│   ├── go.json               # Go platform pack
│   ├── node.json             # Node.js platform pack
│   ├── python.json           # Python platform pack
│   └── generic.json          # Fallback pack (minimal assumptions)
├── bootstrap/
│   ├── bootstrap.tpl.md      # Single consolidated template
│   ├── renderer.go           # Template rendering
│   ├── renderer_test.go      # Template binding tests
│   └── data.go               # TemplateData struct
└── renderer.go               # Shared renderer utilities
```

### Pack JSON Schema

```json
{
  "name": "go",
  "display_name": "Go",
  "version": "1.0.0",
  "language_version": "1.23",

  "recommended_base_image": "golang:${LANGUAGE_VERSION}-alpine",

  "tooling": {
    "package_manager": "go mod",
    "linter": "golangci-lint",
    "test_framework": "go test",
    "formatter": "gofmt"
  },

  "makefile_targets": {
    "build": "go mod tidy && go build -o bin/${PROJECT_NAME} ./...",
    "test": "go test ./...",
    "lint": "golangci-lint run",
    "run": "go run ./...",
    "clean": "rm -rf bin/ && go clean"
  },

  "template_sections": {
    "module_setup": "... markdown template fragment ...",
    "lint_config": "... embedded config example ...",
    "quality_setup": "... markdown template fragment ..."
  }
}
```

**Required Fields:**
- `name` - Pack identifier
- `version` - Semantic version of the pack
- `display_name` - Human-readable name
- `makefile_targets.build` - Build command
- `makefile_targets.test` - Test command
- `makefile_targets.lint` - Lint command (required for quality gates)
- `makefile_targets.run` - Run command (required for demo mode)

**Optional Fields:**
- `language_version` - Default language version
- `recommended_base_image` - Base Docker image
- `tooling.*` - Informational tooling names
- `makefile_targets.clean`, `.install` - Additional targets
- `template_sections.*` - Markdown fragments for insertion

### Key Design Decisions

1. **Packs in `pkg/templates/packs/`** - Not under `bootstrap/` because packs will ultimately contain data used beyond bootstrap (system prompts, etc.)

2. **Embedded JSON files** - Using `//go:embed` for easy viewing, validation, and updates while keeping them compiled into the binary

3. **Semantic versioning** - Each pack has a version that gets stored in `config.json` so we can detect outdated configurations

4. **Generic fallback** - If a platform has no pack or pack validation fails, fall back to `generic.json` with warnings

5. **Makefile-centric** - Commands in packs are what goes INTO Makefile targets, not raw shell invocations. Maestro standardizes on Make across platforms.

6. **Template sections** - Platform-specific markdown fragments inserted at designated points, avoiding large conditionals in the base template

### Config.json Addition

```json
{
  "platform": "go",
  "pack_version": "1.0.0"
}
```

This enables future "pack updated since bootstrap" detection. MVP: record only, no upgrade mechanics.

## Test Strategy

### Pack Validation Tests (`packs_test.go`)

| Test | Purpose |
|------|---------|
| `TestAllPacksValid` | Load every embedded JSON, validate schema |
| `TestPackRequiredFields` | Ensure name, version, display_name, build, test present |
| `TestGenericPackExists` | generic.json must exist as fallback |
| `TestInvalidPackFallsBackToGeneric` | Validator returns generic on bad pack |
| `TestUnknownTokenDetection` | Tokens like `${LANG_VER}` cause validation error |
| `TestAllowedTokensPass` | `${PROJECT_NAME}` and `${LANGUAGE_VERSION}` pass validation |

### Template Binding Tests (`renderer_test.go`)

| Test | Purpose |
|------|---------|
| `TestAllPacksBindToTemplate` | Every valid pack renders without empty placeholders |
| `TestBootstrapDataBindsCorrectly` | TemplateData fields populate template |
| `TestEmptyFieldsGetDefaults` | Missing fields fall back to pack/generic defaults |
| `TestNoUnrenderedTokens` | No `${` remains after token replacement |
| `TestNoUnrenderedPlaceholders` | No `{{` or empty backticks in output |
| `TestMissingLanguageVersionError` | Pack using `${LANGUAGE_VERSION}` with empty value → error |

### Golden File Tests (`renderer_test.go`)

| Test | Purpose |
|------|---------|
| `TestGoldenOutput_Generic` | Snapshot test for generic pack output |
| `TestGoldenOutput_Go` | Snapshot test for go pack output |

Golden files live in `pkg/templates/bootstrap/testdata/` and are checked into the repo. Tests compare rendered output against golden files and fail on mismatch.

### Pack Validator

```go
// ValidatePack checks a pack has all required fields and valid tokens
// Returns (validatedPack, warnings, error)
// On critical error: returns generic pack + warning
func ValidatePack(pack *Pack) (*Pack, []string, error)
```

**Required fields** (cause fallback to generic if missing):
- `name`
- `version`
- `display_name`
- `makefile_targets.build`
- `makefile_targets.test`

**Token validation** (cause validation error):
- Unknown tokens (not in allowlist)

**Warning fields** (logged but don't cause fallback):
- `tooling.linter` (empty is ok for some platforms)
- `template_sections` (can be empty)

## Future Scope

This implementation is scoped to bootstrap only. Future enhancements:

1. **System prompt packs** - Platform-specific guidance for coder agents
2. **Template Makefiles** - Full Makefile templates per platform, not just target commands
3. **Pack updates** - CLI command to check for and apply pack updates
4. **Custom packs** - User-defined packs in `.maestro/packs/`

---

## Implementation Progress

### Todo List

**Phase 1: Pack Infrastructure**
- [ ] Create `pkg/templates/packs/` directory structure
- [ ] Implement pack loader with JSON parsing (`packs.go`)
- [ ] Implement pack validator (required fields + token validation)
- [ ] Implement token replacement in `Pack.Rendered(TemplateData)`
- [ ] Create `generic.json` pack
- [ ] Create `go.json` pack
- [ ] Write pack validation tests (`packs_test.go`)

**Phase 2: Template Consolidation**
- [ ] Consolidate bootstrap template (`bootstrap.tpl.md`) with section guards
- [ ] Remove `golang.tpl.md` (functionality moved to pack + consolidated template)
- [ ] Update `data.go` to integrate pack data into TemplateData
- [ ] Update `renderer.go` to use packs and call `Pack.Rendered()`

**Phase 3: Testing**
- [ ] Write template binding tests (`renderer_test.go`)
- [ ] Create golden file test infrastructure
- [ ] Create `testdata/golden_generic.md`
- [ ] Create `testdata/golden_go.md`
- [ ] Write golden file comparison tests

**Phase 4: Integration**
- [ ] Add `pack_version` to config schema
- [ ] Update bootstrap tool to use new pack system
- [ ] End-to-end test: bootstrap with generic pack
- [ ] End-to-end test: bootstrap with go pack

**Phase 5: Additional Packs (post-MVP, non-blocking)**
- [ ] Create `node.json` pack (minimal)
- [ ] Create `python.json` pack (minimal)

### Completed

**Phase 1: Pack Infrastructure**
- [x] Create `pkg/templates/packs/` directory structure
- [x] Implement pack loader with JSON parsing (`packs.go`)
- [x] Implement pack validator (required fields + token validation)
- [x] Implement token replacement in `Pack.Rendered(TemplateData)`
- [x] Create `generic.json` pack
- [x] Create `go.json` pack
- [x] Write pack validation tests (`packs_test.go`)

**Phase 2: Template Consolidation**
- [x] Consolidate bootstrap template (`bootstrap.tpl.md`) with section guards
- [x] Remove `golang.tpl.md` (functionality moved to pack + consolidated template)
- [x] Update `data.go` to integrate pack data into TemplateData
- [x] Update `renderer.go` to use packs and call `Pack.Rendered()`

**Phase 3: Testing**
- [x] Write template binding tests (`renderer_test.go`)
- [x] Create golden file test infrastructure
- [x] Create `testdata/golden_generic.md`
- [x] Create `testdata/golden_go.md`
- [x] Write golden file comparison tests

**Phase 4: Integration**
- [x] Add `pack_version` and `pack_name` fields to config schema (`ProjectInfo` struct)
- [x] Add `UpdateProjectPack()` and `GetProjectPack()` functions to config package
- [x] Update bootstrap tool to store pack version during bootstrap

---

*Created: 2025-01-11*
*Branch: rc2*
