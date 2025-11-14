# PM Bootstrap Integration - Technical Reference

## Quick Reference for Implementation

### Key Interfaces and Types

```go
// pkg/pm/bootstrap_detector.go
type BootstrapDetector struct {
    projectDir string
    logger     *logx.Logger
}

type BootstrapRequirements struct {
    NeedsGitRepo        bool
    NeedsDockerfile     bool
    NeedsMakefile       bool
    NeedsKnowledgeGraph bool
    NeedsBuildTargets   []string  // build, test, lint, run
    DetectedPlatform    string    // go, python, node, generic
    PlatformConfidence  float64   // 0.0 to 1.0
    MissingComponents   []string  // Human-readable list
}

type BootstrapContext struct {
    Expertise         string  // NON_TECHNICAL, BASIC, EXPERT
    HasRepo          bool
    HasDockerfile    bool
    HasMakefile      bool
    Platform         string
    ProjectDir       string
}
```

### PM State Extensions

```go
// Additional state data keys for PM
const (
    StateKeyHasRepository    = "has_repository"
    StateKeyBootstrapReqs    = "bootstrap_requirements"
    StateKeyDetectedPlatform = "detected_platform"
    StateKeyExpertiseLevel   = "user_expertise"
)
```

### Default Knowledge Graph Content

```go
const DefaultKnowledgeGraph = `digraph ProjectKnowledge {
    // Core patterns that apply to all projects
    "error-handling" [
        type="pattern"
        level="implementation"
        status="current"
        description="Use consistent error handling with context"
        example="Wrap errors with meaningful context messages"
    ];

    "code-style" [
        type="rule"
        level="implementation"
        status="current"
        description="Follow language-specific style guides"
        priority="high"
    ];

    "testing" [
        type="pattern"
        level="implementation"
        status="current"
        description="Write tests for all new functionality"
        example="Unit tests for functions, integration tests for APIs"
    ];

    "documentation" [
        type="rule"
        level="architecture"
        status="current"
        description="Document all public APIs and major design decisions"
        priority="high"
    ];

    "security" [
        type="rule"
        level="architecture"
        status="current"
        description="Follow security best practices"
        priority="critical"
    ];
}`
```

### Platform Detection Patterns

```go
// Platform detection file patterns
var PlatformIndicators = map[string][]string{
    "go": {
        "go.mod",
        "go.sum",
        "*.go",
        "vendor/",
    },
    "python": {
        "requirements.txt",
        "pyproject.toml",
        "setup.py",
        "Pipfile",
        "*.py",
        "__pycache__/",
        ".venv/",
        "venv/",
    },
    "node": {
        "package.json",
        "package-lock.json",
        "yarn.lock",
        "pnpm-lock.yaml",
        "node_modules/",
        "*.js",
        "*.ts",
        "*.jsx",
        "*.tsx",
    },
    "rust": {
        "Cargo.toml",
        "Cargo.lock",
        "*.rs",
        "target/",
    },
    "java": {
        "pom.xml",
        "build.gradle",
        "*.java",
        ".gradle/",
    },
    "csharp": {
        "*.csproj",
        "*.sln",
        "*.cs",
        "obj/",
        "bin/",
    },
}
```

### Bootstrap Spec Template Structure

```markdown
## Bootstrap Requirements

### 1. Initialize Documentation System
- Create `.maestro/` directory if not exists
- Create `.maestro/knowledge.dot` with default knowledge graph
- Initialize with core patterns for consistency

### 2. Git Repository Setup
- ${GIT_SETUP_DETAILS}

### 3. Development Container
- ${CONTAINER_DETAILS}

### 4. Build System Configuration
- ${BUILD_SYSTEM_DETAILS}

### 5. Project Structure
- ${PROJECT_STRUCTURE_DETAILS}

## Project Requirements
${USER_REQUIREMENTS}
```

### Expertise Question Mapping

```go
// Question counts by expertise level
var ExpertiseQuestions = map[string]QuestionSet{
    "NON_TECHNICAL": {
        MinQuestions: 3,
        MaxQuestions: 4,
        SkipTechnical: true,
        AutoDetect: true,
    },
    "BASIC": {
        MinQuestions: 5,
        MaxQuestions: 7,
        ConfirmDetected: true,
        AutoDetect: true,
    },
    "EXPERT": {
        MinQuestions: 8,
        MaxQuestions: 12,
        FullControl: true,
        AutoDetect: false,
    },
}
```

### Critical File Paths

```go
// Key file locations in the project
const (
    KnowledgeGraphPath = ".maestro/knowledge.dot"
    MakefilePath       = "Makefile"
    DockerfilePath     = "Dockerfile"
    GitIgnorePath      = ".gitignore"
    GitAttributesPath  = ".gitattributes"
    EditorConfigPath   = ".editorconfig"
    MirrorDirPath      = ".mirrors/"
)
```

### Required Makefile Targets

```makefile
# These targets MUST be present
.PHONY: build test lint run

build:
	# Platform-specific build commands

test:
	# Platform-specific test commands

lint:
	# Platform-specific lint commands

run:
	# Platform-specific run commands
```

### Container Strategy Decision Tree

```
if HasCustomDockerfile:
    if ExpertiseLevel == "EXPERT":
        Ask about using custom Dockerfile
    else:
        Use custom Dockerfile automatically
else:
    if Platform == "go":
        Base = "golang:1.21-alpine"
    elif Platform == "python":
        Base = "python:3.11-slim"
    elif Platform == "node":
        Base = "node:18-alpine"
    else:
        Base = "maestro-bootstrap:latest"
```

### Error Messages

```go
// User-friendly error messages by expertise
var ErrorMessages = map[string]map[string]string{
    "NON_TECHNICAL": {
        "no_repo": "I need a place to store your code. Please create a GitHub repository and share the URL with me.",
        "no_platform": "I'm analyzing your project to understand what technology to use.",
    },
    "BASIC": {
        "no_repo": "Please create a GitHub repository for this project (e.g., github.com/yourname/projectname)",
        "no_platform": "Unable to detect project platform. What programming language would you like to use?",
    },
    "EXPERT": {
        "no_repo": "Repository required. Create at: github.com/org/repo",
        "no_platform": "Platform detection failed. Specify target platform.",
    },
}
```

### Testing Scenarios

```go
// Key test cases to cover
var TestScenarios = []Scenario{
    {Name: "No repo, no files", Expected: "Full bootstrap"},
    {Name: "Repo exists, no Dockerfile", Expected: "Container bootstrap"},
    {Name: "Everything exists", Expected: "No bootstrap, straight to requirements"},
    {Name: "Partial Makefile", Expected: "Add missing targets"},
    {Name: "No knowledge graph", Expected: "Initialize documentation"},
    {Name: "Wrong platform detected", Expected: "User correction accepted"},
}
```

### Configuration Updates

```json
// New config fields for PM bootstrap
{
    "pm": {
        "enabled": true,
        "default_expertise": "BASIC",
        "bootstrap_detection": true,
        "auto_create_repo": false  // Future enhancement
    }
}
```

### Git Repository Validation

```go
// Check if repo URL is valid and accessible
func ValidateGitRepository(url string) error {
    // Parse URL format
    // Check GitHub/GitLab/Bitbucket patterns
    // Optionally verify with git ls-remote
    return nil
}
```

### State Transition Updates

```go
// PM state transitions with bootstrap
StateWaiting -> StateAwaitUser:    "Start interview (with bootstrap detection)"
StateWorking -> StateAwaitUser:     "Need bootstrap information"
StateWorking -> StatePreview:       "Bootstrap + requirements ready for review"
```

## Implementation Checklist

### Before Starting
- [ ] Review `pkg/workspace/pm.go` current implementation
- [ ] Understand PM state machine in `pkg/pm/states.go`
- [ ] Review existing bootstrap in `cmd/maestro/interactive_bootstrap.go`
- [ ] Check knowledge graph spec in `docs/DOC_GRAPH.md`

### During Implementation
- [ ] Keep PM read-only (no file modifications)
- [ ] Ensure knowledge graph is first in bootstrap
- [ ] Test all expertise levels
- [ ] Validate container strategy works
- [ ] Handle missing repository gracefully

### After Implementation
- [ ] Remove legacy bootstrap code
- [ ] Update all documentation
- [ ] Run full test suite
- [ ] Verify WebUI integration
- [ ] Test end-to-end flow

## Common Pitfalls to Avoid

1. **Don't let PM write files** - Only coders have write access
2. **Don't skip knowledge graph** - It must be initialized first
3. **Don't assume repo exists** - Handle missing repos gracefully
4. **Don't ignore expertise** - Respect user's technical level
5. **Don't break existing PM** - Maintain backward compatibility until cleanup
6. **Don't trust platform detection** - Always allow user override
7. **Don't hardcode paths** - Use configuration and constants

## Quick Command Reference

```bash
# Test PM without repository
rm -rf .maestro/config.json
maestro init  # Should start PM without repo

# Test with existing repo but no bootstrap
git init test-repo
cd test-repo
maestro init  # Should detect missing bootstrap

# Test expertise levels
MAESTRO_PM_EXPERTISE=NON_TECHNICAL maestro init
MAESTRO_PM_EXPERTISE=EXPERT maestro init

# Verify knowledge graph initialization
cat .maestro/knowledge.dot  # Should exist after bootstrap
```

## Integration Points

### With Architect
- Architect receives spec with bootstrap requirements
- Bootstrap tasks assigned to coders like any other requirement
- No special handling needed by architect

### With Coders
- Coders implement bootstrap requirements first
- Knowledge graph initialization is priority #1
- Standard implementation and testing flow

### With WebUI
- Interview start includes expertise selector
- No separate bootstrap button
- Progress shows bootstrap tasks separately

This technical reference provides the key information needed during implementation without diving into full code details.