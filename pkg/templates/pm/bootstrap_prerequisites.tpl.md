# Bootstrap Prerequisites

The following infrastructure requirements must be completed before implementing user features. These are system prerequisites that enable the development framework to function.

### MANDATORY PREREQUISITE R-001: Initialize Knowledge Graph
**Type:** infrastructure
**Priority:** must
**Dependencies:** []

**Description:**
Create `.maestro/knowledge.dot` file with initial architectural patterns and rules. This establishes the foundational documentation structure for the project. The knowledge graph is REQUIRED for the architect to function - it cannot operate without this file.

The default knowledge graph includes six core patterns and rules:
- **error-handling**: Pattern for wrapping errors with context using fmt.Errorf
- **api-standards**: Rule for REST API OpenAPI 3.0 compliance
- **test-coverage**: Rule requiring 80% minimum test coverage (critical priority)
- **code-style**: Pattern for following language-specific style guides
- **logging-standards**: Pattern for structured logging with appropriate levels
- **security-headers**: Rule for HTTP security headers (critical priority)

**Acceptance Criteria:**
- [ ] File created at `.maestro/knowledge.dot`
- [ ] Contains valid DOT format digraph named "ProjectKnowledge"
- [ ] Includes six default nodes with proper attributes (type, level, status, description)
- [ ] Two nodes marked as critical priority (test-coverage, security-headers)
- [ ] Platform-agnostic content suitable for any project
- [ ] File matches DOC_GRAPH.md specification format

{{if not .Extra.HasRepository}}
### MANDATORY PREREQUISITE R-002: Configure Git Repository
**Type:** infrastructure
**Priority:** must
**Dependencies:** [R-001]

**Description:**
Configure the project's GitHub repository (URL configured via bootstrap tool).
Ensure repository is initialized and accessible for development workflow. Git repository is REQUIRED for the architect and coders to commit and merge code.

**Acceptance Criteria:**
- [ ] Repository URL configured in `.maestro/config.json`
- [ ] Repository is accessible and authenticated
- [ ] Initial commit with project structure
{{end}}

{{if .Extra.NeedsDockerfile}}
### MANDATORY PREREQUISITE R-003: Create and Configure Development Container
**Type:** infrastructure
**Priority:** must
**Dependencies:** [R-001, R-002]

**Description:**
Create Dockerfile for {{.Extra.DetectedPlatform}} development environment, build the container image, and configure it as the target container for coder agents.
Container provides consistent build and test environment for all developers. Container configuration is REQUIRED for coders to execute code in isolated environments.

Coders have access to container management tools:
- `container_build` - Build Docker images from Dockerfile
- `container_test` - Test containers before making them active
- `container_update` - Set the built container as the target image for all coders

**Acceptance Criteria:**
- [ ] Dockerfile created with {{.Extra.DetectedPlatform}} base image
- [ ] Development dependencies installed (compilers, build tools, linters)
- [ ] Build tools configured and tested
- [ ] Container builds successfully using `container_build`
- [ ] Container tested and validated using `container_test`
- [ ] Container configured as target image using `container_update` tool
{{end}}

{{if .Extra.NeedsMakefile}}
### MANDATORY PREREQUISITE R-004: Create Build System (Makefile)
**Type:** infrastructure
**Priority:** must
**Dependencies:** [R-001, R-002]

**Description:**
Create Makefile with standard targets for {{.Extra.DetectedPlatform}} project: build, test, lint, run.
Provides consistent interface for development operations. Makefile is REQUIRED for coders to build, test, and run code.

**Acceptance Criteria:**
- [ ] Makefile with `build` target
- [ ] Makefile with `test` target
- [ ] Makefile with `lint` target
- [ ] Makefile with `run` target
- [ ] All targets work in development container
{{end}}

---

# User Requirements
