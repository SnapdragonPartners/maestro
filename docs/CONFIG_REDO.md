# Config-Driven Project Initialization System

## **Problem Statement**

Current issues with Maestro's project setup:
- Platform detection runs every orchestrator start (performance)
- Inconsistent backend selection (project vs story level)  
- No user control over project settings
- Settings scattered across CLI, detection, hardcoded defaults
- Bootstrap process is deterministic but inflexible
- Container selection chicken-and-egg problem during bootstrap

## **Solution Overview**

Replace scattered configuration and repeated platform detection with unified, config-driven approach using:
- Two-tier configuration system (user + project configs)
- `maestro init` command for interactive project setup
- Story-driven bootstrap with validation requirements
- Two-phase container strategy to solve chicken-and-egg problem

## **Two-Tier Configuration System**

### **Configuration Precedence (High to Low)**
1. Project config (`<project>/.maestro/config.json`)
2. User defaults (`~/.maestro/config.json`)  
3. Built-in defaults

*(No CLI flags for everything, no env var overrides - keep it simple)*

### **Config Schema**
```json
{
  "schema_version": "1.0",
  "project": {
    "name": "my-project",
    "git_repo": "git@github.com:user/repo.git",
    "platform": "go",
    "platform_confidence": 0.98,
    "created_at": "2025-01-27T19:30:00Z"
  },
  "container": {
    "image": "golang:1.24-alpine",
    "dockerfile": "Dockerfile-Maestro",
    "dockerfile_hash": "a1b2c3d4e5f6789...",
    "image_tag": "maestro-myproject:latest", 
    "needs_rebuild": false,
    "last_built": "2025-01-27T19:30:00Z"
  },
  "build": {
    // REQUIRED targets (can be overridden but must exist)
    "build": "make build",
    "test": "make test", 
    "lint": "make lint",
    "run": "make run",
    
    // Optional extras
    "clean": "make clean",
    "install": "make install"
  },
  "bootstrap": {
    "completed": true,
    "last_run": "2025-01-27T19:30:00Z",
    "requirements_met": {
      "makefile_validated": true,
      "container_built": true,
      "git_repo_accessible": true
    }
  }
}
```

### **Agent Config Access**
- Each agent holds reference to shared config object
- Mutex-protected reads/writes for coordination
- Agents can call `config.SetContainerNeedsRebuild()` when Dockerfile changes
- Agents can read build commands via `config.GetBuildCommand("test")`

## **Maestro Init Command**

### **Entry Points**
1. **`maestro init`** - Interactive wizard (default)
2. **`maestro init --defaults`** - Non-interactive with sensible defaults  
3. **`maestro init --file template.json`** - Seed from template

### **Interactive Flow**
1. Load user defaults from `~/.maestro/config.json`
2. Auto-detect platform where possible
3. Prompt for required fields:
   - Git repository URL (required)
   - Confirm detected platform  
   - Container preference (default image vs generated Dockerfile-Maestro)
4. Validate merged config using JSON Schema
5. Write project config to `<project>/.maestro/config.json`
6. Check if bootstrap can be bypassed (all requirements already met)
7. If needed, inject "Story 0" bootstrap into architect

## **Bootstrap System**

### **Two-Phase Container Strategy**
1. **Bootstrap Phase**: Always use `ubuntu:22.04` (fallback container)
   - Platform-agnostic environment for setup tasks
   - Can install tools, run commands, modify files
   - Solves chicken-and-egg problem with container selection

2. **Development Phase**: Use determined container from bootstrap
   - Bootstrap validates and builds proper container
   - Subsequent coders use the validated container

### **Bootstrap Story Approach**
- **Method**: Standard coder with bootstrap story injection (no special FSM)
- **Story ID**: `000.md` (reserved, ensures ordering)
- **Tools**: `shell`, `modify_config`, `done`
- **Container**: Uses `ubuntu:22.04` for platform-agnostic setup

### **Bootstrap Validation Requirements**

#### **A) Build System Validation** ✅ **REQUIRED**
- Makefile exists with working `build`, `test`, `lint` targets
- Each target executes successfully
- Creates default Makefile if missing
- **Validation**:
  ```bash
  test -f Makefile || create_default_makefile()
  make build && make test && make lint
  ```

#### **B) Container Preparation** ✅ **REQUIRED**  
- Generate/validate `Dockerfile-Maestro` based on platform
- Build container: `docker build -f Dockerfile-Maestro -t maestro-<project>:latest .`
- Test container works (smoke test)
- Calculate MD5 hash for change tracking
- **Example Generated Dockerfile**:
  ```dockerfile
  FROM golang:1.24-alpine
  RUN apk add --no-cache make git curl
  RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
  WORKDIR /workspace
  COPY go.mod go.sum ./
  RUN go mod download
  CMD ["make", "build"]
  ```

#### **C) Git Repository Validation** ✅ **REQUIRED**
- Git repo URL is accessible (clone/fetch test)
- Authentication works (SSH keys, tokens, etc.)
- Can create mirror successfully
- **Validation**:
  ```bash
  git ls-remote $GIT_REPO_URL
  git clone --mirror $GIT_REPO_URL /tmp/test-mirror
  rm -rf /tmp/test-mirror
  ```

### **Bootstrap Story Structure**
```markdown
# Bootstrap Project Setup (000.md)

**Task**: Validate and prepare project for development

**Acceptance Criteria**:
- [ ] Makefile exists with working build, test, lint targets
- [ ] Docker container is built and validated  
- [ ] Git repository is accessible and can be mirrored
- [ ] Configuration file is updated with validated settings

**Implementation Process**:
1. Validate/create Makefile with required targets
2. Generate Dockerfile-Maestro for detected platform
3. Build and test Docker container
4. Verify git repository accessibility  
5. Update config with validation results
```

### **Bootstrap Bypass Logic**
Skip bootstrap if ALL requirements already met:
- `config.bootstrap.completed == true`
- `config.bootstrap.requirements_met.makefile_validated == true`
- `config.bootstrap.requirements_met.container_built == true`
- `config.bootstrap.requirements_met.git_repo_accessible == true`
- Dockerfile hash matches current file (no changes)

## **Container Selection Logic**

### **Updated Priority**
1. **Custom Dockerfile**: If `container.dockerfile` exists, use `container.image_tag`
2. **Explicit Image**: Use `container.image` from config
3. **Platform Default**: Fallback to platform-specific image
4. **Universal Default**: `ubuntu:22.04` (bootstrap only)

### **Container Change Detection**
```go
func (c *Config) ContainerNeedsRebuild() bool {
    if c.Container.Dockerfile == "" {
        return false // Using pre-built image
    }
    currentHash := calculateMD5(c.Container.Dockerfile)
    return currentHash != c.Container.DockerfileHash
}
```

### **Build Context**
- **Build Root**: `<workdir>` (the git repo directory)  
- **Dockerfile Location**: `<workdir>/Dockerfile-Maestro`
- **Build Command**: `docker build -f Dockerfile-Maestro -t maestro-<project>:latest <workdir>`

## **Implementation Roadmap**

### **Story 070: Config Foundation**
- Define `UserConfig` and `ProjectConfig` Go structs
- Implement JSON Schema validation
- Mutex-protected config access for agents
- Container rebuild detection mechanism
- Config loader with proper precedence handling

### **Story 071: Maestro Init Command**  
- Interactive project setup with prompts
- Bootstrap story generation and injection into architect
- User defaults loading and merging
- Project config creation with validation
- Bootstrap bypass detection

### **Story 072: Bootstrap Story System**
- Bootstrap story template with validation requirements
- `modify_config` tool implementation for config updates
- Makefile detection/creation logic with required targets
- Container building and validation in ubuntu:22.04 environment
- Git repository accessibility testing

### **Story 073: Agent Integration**
- Replace platform detection with config loading throughout codebase
- Container selection using rebuild tracking
- Agent config access implementation with mutex protection
- Build command resolution from config (`config.GetBuildCommand("test")`)
- Remove inconsistent story-level backend detection

### **Story 074: Advanced Features**
- Non-interactive init variants (`--defaults`, `--file`)
- Config management commands (`maestro config show/set/validate`)  
- Container rebuild workflow when Dockerfile changes
- Migration tools for schema updates

## **Key Benefits**

### **Performance**
- ✅ One-time platform detection (cached in config)
- ✅ No repeated file system scanning
- ✅ Faster orchestrator startup
- ✅ Smart bootstrap bypass when requirements already met

### **Consistency** 
- ✅ Single source of truth for all project settings
- ✅ Unified container selection across all stories
- ✅ Reproducible builds across environments
- ✅ Required build targets always available

### **User Control**
- ✅ Editable configuration files outside database
- ✅ Custom container support via Dockerfile-Maestro
- ✅ User defaults for common preferences
- ✅ Interactive setup for new projects
- ✅ Smart defaults with override capability

### **Maintainability**
- ✅ Clean separation of concerns
- ✅ JSON Schema validation prevents misconfigurations  
- ✅ Versioned schema for future migrations
- ✅ Story-driven bootstrap allows flexible project setup
- ✅ No backwards compatibility burden (pre-release)

## **Implementation Notes**

### **Makefile-First Philosophy**
- Default expectation: Makefile with `build`, `test`, `lint`, `run` targets
- Bootstrap creates if missing, validates if present
- Config can override commands but targets must exist
- Leverages existing build system knowledge

### **No Complex CLI/Environment Override**
- Keep configuration simple and predictable
- File-based configuration is explicit and versionable
- Reduces complexity and edge cases

### **Bootstrap Failure Handling**
- Clear error messages for each validation failure
- Allow partial completion and retry
- `maestro init --retry` to re-run failed bootstrap steps
- Graceful degradation where possible

---

## **Status: Ready for Implementation**

This design addresses all current issues while providing a foundation for flexible, user-controlled project management. The two-phase container approach solves the chicken-and-egg problem, and the story-driven bootstrap provides the flexibility needed for diverse project types.

**Next Step**: Implement Story 070 (Config Foundation)