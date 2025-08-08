# Bootstrap System Redesign - Implementation Tasks

## Overview
Redesigning bootstrap system to be architect-integrated, Makefile-centric, with proper Git integration and multi-platform support. Bootstrap is now triggered by spec receipt and integrated into the architect's SCOPING state.

## Core Architecture Changes

### 1. Architect-Integrated Bootstrap Flow
- [x] **Load architect before bootstrap phase** 
- [x] **Create LLM prompt for stack analysis**
  - Prompt: "Determine what technology stack the user intended from this specification. If none is specified, make a recommendation based on the requirements. Consider that projects often use multiple technologies (e.g., Go backend + React frontend)."
- [x] **Add validation for architect recommendations**
  - Validate against explicit technology keywords in spec
  - Score confidence of recommendation
- [ ] **Integrate platform detection into architect SCOPING state**
  - Run platform detection on existing code first
  - If platform detected → add to scoping context
  - If no platform → LLM recommends from spec
  - If confident → save platform, trigger bootstrap
  - If uncertain → transition to ERROR with clarifying questions
- [ ] **Add QUESTION channel for ambiguous specs**
  - Block bootstrap until clarification received
- [ ] **Support semantic version detection**
  - Extract version requirements from spec (Go 1.24, Node 20, etc.)

### 2. Platform Whitelist & Validation
- [x] **Create supported platform whitelist**
  - Initial: go, node, python, make, null
  - Future: rust, java, docker, etc.
- [x] **Add LLM hallucination prevention**
  - Score exotic stacks against whitelist
  - Require human approval for non-whitelisted platforms
- [x] **Add fallback to safe defaults**
  - Default to "go" or "null" for low-confidence recommendations

### 3. Bootstrap Integration into Architect SCOPING
- [ ] **Move bootstrap trigger from orchestrator to architect**
  - Remove bootstrap from orchestrator startup
  - Integrate platform detection into SCOPING state
- [ ] **Enhanced SCOPING state workflow**
  - Step 1: Run platform detection on existing code
  - Step 2: If platform detected → add to scoping context
  - Step 3: If no platform → LLM recommends from spec
  - Step 4: If confident → save platform, trigger bootstrap
  - Step 5: If uncertain → transition to ERROR with questions
- [ ] **Bootstrap execution within SCOPING**
  - Create bootstrap worktree (`bootstrap-init` branch)
  - Generate `.maestro` directory structure
  - Commit bootstrap artifacts to Git
  - Continue to story generation with platform context

### 4. .maestro Directory Structure
- [x] **Create `.maestro/` directory for artifacts**
- [x] **Move generated files to `.maestro/`**
  - `.maestro/makefiles/core.mk`
  - `.maestro/makefiles/go.mk`
  - `.maestro/makefiles/node.mk`
  - `.maestro/makefiles/python.mk`
  - `.maestro/config.json` (optional)
- [x] **Update root Makefile to include from `.maestro/`**
  ```makefile
  include .maestro/makefiles/core.mk
  include .maestro/makefiles/go.mk
  # etc.
  ```

### 5. Makefile-Centric Build System
- [x] **Update all build backends to use Makefile**
  - Change from direct `go test` to `make test`
  - Change from direct `npm test` to `make test`
  - Never bypass Makefile with direct toolchain calls
- [x] **Create modular Makefile generation**
  - `makefiles/core.mk` - generated once, rarely edited
  - `makefiles/{platform}.mk` - language-specific templates
  - Root Makefile as thin facade with includes
- [x] **Add sentinel comments for generated blocks**
  - `### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT`
  - Preserve human edits outside generated blocks
- [x] **Self-test Makefiles before committing**
  - Run `make test` on empty stub project
  - Ensure syntactic validity

### 6. Stack Evolution Support
- [ ] **Add STACK_ADD message type**
  - For adding technologies after initial bootstrap
- [ ] **Create bootstrap-update workflow**
  - Schedule bootstrap-update tasks via dispatcher
  - Generate new `.maestro/makefiles/{platform}.mk`
  - Update root Makefile includes
- [ ] **Create stack manifest file**
  - `.maestro/stack.yaml` or `.maestro/bootstrap.lock`
  - Track current stacks & versions
  - Enable diff between desired vs current state
- [ ] **Make stack changes additive**
  - Never remove existing blocks unless explicitly instructed
  - Support coexistence of multiple technologies

### 7. Cross-Platform & Tool Support
- [x] **Add cross-platform Makefile compatibility**
  - Avoid Bash-only commands
  - Use portable Make rules or Go helpers
- [x] **Add version pinning support**
  - Generate `.tool-versions` (asdf)
  - Generate `.nvmrc` for Node
  - Update `go.mod` go directive
- [x] **Add security scanning targets**
  - `make scan` wired to `npm audit`, `gosec`, etc.
  - Optional but wire once rather than retrofit

### 8. Error Handling & Recovery
- [ ] **Add bootstrap failure detection**
  - Detect repeated bootstrap branch failures
  - Auto-escalate to human intervention
- [ ] **Add existing code conflict detection**
  - Check for non-empty directories (`go.mod`, `package.json`)
  - Present merge strategies (replace/coexist)
- [ ] **Add bootstrap rollback capability**
  - Ability to revert bad bootstrap artifacts

## Implementation Order
1. **Phase 1**: Architect-first backend determination + platform whitelist ✅
2. **Phase 2**: `.maestro` directory structure + modular Makefiles ✅
3. **Phase 3**: Bootstrap integration into architect SCOPING state 🔄
4. **Phase 4**: Stack evolution support + idempotency
5. **Phase 5**: Cross-platform support + tooling integration

## Current Architecture: Spec-Driven Bootstrap
Bootstrap is now triggered by spec receipt and integrated into the architect's SCOPING state:

1. **Orchestrator Startup**: No bootstrap - just start agents in WAITING state
2. **Spec Receipt**: Web UI or CLI provides spec to architect
3. **SCOPING State Enhanced**: 
   - Run platform detection on existing code
   - If platform detected → add to scoping context
   - If no platform → LLM recommends from spec  
   - If confident → save platform, trigger bootstrap
   - If uncertain → transition to ERROR with questions
4. **Bootstrap Execution**: Create `.maestro` structure, commit artifacts
5. **Story Generation**: Stories include platform context, sent to storyCh
6. **Coding Agents**: Naturally blocked until bootstrap completes

## Testing Strategy
- [ ] **Test with empty projects** (new project bootstrap)
- [ ] **Test with existing projects** (conflict detection)
- [ ] **Test multi-platform projects** (Go + Node, Python + React)
- [ ] **Test stack evolution** (adding platforms later)
- [ ] **Test idempotency** (rerunning bootstrap)
- [ ] **Test cross-platform** (Windows compatibility)

## Future Considerations
- Docker/dev-container generation
- More sophisticated linter/manager configurability (uv, venv)
- IDE integration (.vscode, .idea)
- CI/CD pipeline generation