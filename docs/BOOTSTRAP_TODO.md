# Bootstrap System Implementation TODO

**Status**: Phase 2 (Build Backend System) Complete - Moving to Phase 1 & 4 Integration

## Current State

✅ **COMPLETED**:
- Build backend system (`pkg/build/`) with unified `BuildBackend` interface
- MVP backends: Go, Python (uv), Node.js, Make, Null
- Priority-based backend detection and registry
- Comprehensive test suite and documentation
- Coder agent integration (agents detect and run build tools directly)

❌ **MISSING**: 
- Orchestrator-level PROJECT_BOOTSTRAP phase
- Orchestrator build execution endpoints
- Agent refactoring to use orchestrator endpoints

## Phase 1: PROJECT_BOOTSTRAP Orchestrator Phase

### 🔴 Critical (High Priority)

- [ ] **Implement PROJECT_BOOTSTRAP orchestrator phase** (`bootstrap-1`)
  - Add blocking phase that runs before story dispatch
  - Orchestrator must complete bootstrap before any coder tasks are sent
  - Integration with main orchestrator startup flow

- [ ] **Create Story 000 blocking mechanism in dispatcher** (`bootstrap-2`)
  - Dispatcher won't send tasks until bootstrap story is in `DONE` state
  - Special handling for Story 000 as blocking prerequisite
  - Story dependency system integration

- [ ] **Add bootstrap branch creation and auto-merge logic** (`bootstrap-3`)
  - Create dedicated `bootstrap-init` branch for bootstrap artifacts
  - Auto-merge to `main` branch after bootstrap completion
  - Git worktree integration with existing workspace manager

- [ ] **Design bootstrap artifact templates** (`bootstrap-4`)
  - Makefile with include pattern for conflict-free updates
  - Language-specific `.gitignore` files
  - CI workflow templates (`.github/workflows/ci.yaml`)
  - Development environment files (`.editorconfig`, version pinning)

## Phase 4: Orchestrator Build Execution

### 🔴 Critical (High Priority)

- [ ] **Create orchestrator build execution endpoints** (`bootstrap-5`)
  - HTTP/gRPC endpoints for build, test, lint, run operations
  - Streaming output support for real-time feedback
  - Context and timeout handling

- [ ] **Add BuildBackend integration to orchestrator** (`bootstrap-6`)
  - Orchestrator manages backend detection per project
  - Backend selection and caching
  - Connection between endpoints and build backend system

- [ ] **Refactor coder agents to use orchestrator build endpoints** (`bootstrap-7`)
  - Remove direct build tool execution from coder agents
  - Replace `runMakeTest` with orchestrator API calls
  - Update `TESTING` state to use orchestrator endpoints

### 🟡 Medium Priority

- [ ] **Update coder templates to use backend info from TASK payload** (`bootstrap-8`)
  - Templates reference backend-specific information
  - Context about available build operations
  - Language-specific guidance

- [ ] **Add backend name to TASK payload from architect** (`bootstrap-9`)
  - Architect detects backend during story generation
  - Backend information included in task messages
  - Integration with existing task payload structure

- [ ] **Implement backend selection and caching in orchestrator** (`bootstrap-10`)
  - Cache backend detection results per project
  - Lazy loading and invalidation strategies
  - Performance optimization for repeated operations

## Implementation Details

### 🟡 Medium Priority

- [ ] **Create bootstrap configuration options** (`bootstrap-11`)
  - `force_backend` to override auto-detection
  - `skip_makefile` to disable Makefile generation
  - `additional_artifacts` for custom templates
  - `template_overrides` for custom template paths

- [ ] **Add bootstrap artifact generation** (`bootstrap-12`)
  - Makefile with include pattern (`-include agent.mk`)
  - Generated `agent.mk` with backend-specific targets
  - `.gitattributes` with union merge configuration
  - README.md skeleton and CONTRIBUTING.md

- [ ] **Implement NodeBackend and PythonBackend artifact templates** (`bootstrap-13`)
  - Node.js: `package.json` scripts, `.nvmrc`, `eslint` config
  - Python: `pyproject.toml`, `requirements.txt`, `ruff` config
  - Go: `go.mod`, `golangci-lint.yaml`, module structure

## Testing & Validation

### 🟢 Low Priority

- [ ] **Create smoke test: empty repo → bootstrap → health endpoint story** (`bootstrap-14`)
  - End-to-end test of complete bootstrap flow
  - Validates that empty repository becomes fully functional
  - Health endpoint as minimal working application

- [ ] **Test bootstrap with existing projects** (`bootstrap-15`)
  - Go projects with existing `go.mod`
  - Node.js projects with existing `package.json`
  - Python projects with existing `pyproject.toml` or `requirements.txt`

- [ ] **Validate conflict-free Makefile updates** (`bootstrap-16`)
  - Test include file pattern with existing Makefiles
  - Verify union merge strategy prevents conflicts
  - Multiple agents updating build files simultaneously

## Documentation

### 🟢 Low Priority

- [ ] **Update README with bootstrap requirements** (`bootstrap-17`)
  - Document bootstrap phase in orchestrator startup
  - Prerequisites and configuration options
  - Troubleshooting guide for bootstrap failures

- [ ] **Create migration guide for existing projects** (`bootstrap-18`)
  - How to migrate projects with existing Makefiles
  - Handling complex build systems
  - Preserving existing customizations

## Architecture Notes

### Key Design Principles

1. **Orchestrator-Level Build Execution**: Agents request builds from orchestrator instead of running tools directly
2. **Language-Agnostic Interface**: Unified API regardless of underlying toolchain
3. **Conflict-Free Makefile Strategy**: Include file pattern preserves human customizations
4. **Blocking Bootstrap Phase**: Ensures build infrastructure exists before any coding begins

### Integration Points

- **Orchestrator**: Manages bootstrap phase and build execution
- **Dispatcher**: Blocks story dispatch until bootstrap complete
- **Architect**: Detects backend and includes in task payloads
- **Coder**: Uses orchestrator endpoints instead of direct tool execution
- **Workspace Manager**: Handles bootstrap branch creation and merging

### File Structure

```
pkg/
├── build/           # ✅ Build backend system (COMPLETE)
│   ├── backend.go
│   ├── registry.go
│   ├── go_backend.go
│   ├── python_backend.go
│   ├── node_backend.go
│   └── README.md
├── bootstrap/       # ❌ Bootstrap orchestrator phase (TODO)
│   ├── phase.go
│   ├── artifacts.go
│   └── templates/
├── endpoints/       # ❌ Build execution endpoints (TODO)
│   ├── build_api.go
│   └── streaming.go
└── ...
```

## Next Steps

1. **Start with Phase 1**: Implement PROJECT_BOOTSTRAP orchestrator phase
2. **Add Story 000 blocking**: Modify dispatcher to handle bootstrap dependencies
3. **Create build endpoints**: Move build execution to orchestrator level
4. **Refactor agents**: Update coder agents to use orchestrator endpoints
5. **Test integration**: Validate complete bootstrap flow with real projects

The goal is to transform the current agent-level build execution into a proper orchestrator-managed bootstrap system that eliminates the Makefile dependency problem entirely.