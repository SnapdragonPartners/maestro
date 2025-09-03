# DevOps Stories: Container-Aware Infrastructure Development

This document outlines Maestro's approach to handling DevOps stories in a multi-agent environment with containerized development.

## Background

DevOps stories handle infrastructure setup, build pipeline configuration, container management, and deployment preparation. Unlike application stories that work within established environments, DevOps stories often need to modify the development environment itself.

## The Container Transition Challenge

**Problem**: DevOps stories that modify containers, build systems, or infrastructure can invalidate the environment they're running in. For example:
- A story that builds a new application container can't test that container from within the old container
- Multiple DevOps stories running simultaneously can create conflicting environment states
- Stories that install system dependencies may pollute the development environment

**Solution**: Container-aware DevOps development with proper tooling and constraints.

## Architecture Overview

### Two-Container Model

1. **Bootstrap Container** (`maestro-bootstrap`)
   - Safe, stable environment for building and analyzing target containers
   - Contains build tools, Docker, analysis utilities
   - Never modified by stories - always clean and reliable
   - Used for: building target containers, running tests against them, code analysis

2. **Target Container** (from project config, e.g., `maestro-projectname`)
   - The actual application runtime environment
   - Built from Dockerfile in the repository
   - Modified only through Dockerfile changes (never ad-hoc installs)
   - Used for: runtime testing, application execution, final validation

### Container Flow

```
Bootstrap Container
    ├── Analyze existing Dockerfile
    ├── Make Dockerfile modifications  
    ├── Build target container → Target Container
    ├── Test target container ← (external testing)
    ├── Switch to target container (optional) → Target Container
    └── Commit Dockerfile changes
```

## Story Types and Constraints

### DevOps Story Rules

1. **Environment Awareness**: DevOps stories run in bootstrap container by default
2. **Dockerfile-Only Rule**: All target container configuration MUST be in Dockerfile
3. **No Ad-hoc Installation**: Never install packages directly in bootstrap container for target use
4. **Container Switching**: Use `container_switch()` tool to change execution environment
5. **Build-Before-Test**: Always rebuild target container before testing changes

### App Story Rules

1. **Environment Assumption**: App stories assume working infrastructure exists
2. **Dependency Blocking**: App stories depend on DevOps stories that set up their requirements
3. **Container Inheritance**: Use whatever container environment DevOps stories have prepared

## Story Ordering and Container Validation

### Container-First Requirement

**Key Principle**: App stories require a valid target container to run in. If no valid target container exists, a DevOps story must create one first.

### Validation Logic

1. **Runtime Validation**: System maintains `validTargetImage bool` flag (not persisted)
2. **Container Validation**: Checks if configured target container exists, is runnable, and is not the bootstrap container
3. **Story Gate**: If `!validTargetImage`, the first story (in dependency order) must be DevOps type
4. **Dynamic Updates**: `container_update` tool validates and updates the flag when containers are built

### Example Scenarios

**Scenario 1: No Valid Target Container**
```
Stories (dependency order):
1. Container Environment Setup (devops) ← REQUIRED FIRST
2. Go Module Initialization (app)
3. Configure Linting (app) 
4. Setup Makefile (app)
```

**Scenario 2: Valid Target Container Exists** 
```
Stories (any order works):
1. Add Feature X (app)
2. Update Deployment (devops)
3. Implement Tests (app)
```

**Scenario 3: Mixed Development**
```
Stories (dependency order):
1. Update Container Base Image (devops) ← Updates validTargetImage
2. Install New Dependencies (devops) 
3. Implement New Feature (app)
4. Add Integration Tests (app)
5. Update CI Pipeline (devops)
```

## Container Tools

### `container_switch(image_name)`
- Switches coder execution environment to specified container
- Falls back to bootstrap container on failure
- Use `container_switch('maestro-bootstrap')` to return to bootstrap
- Updates context so coder knows its current environment

### `container_build(dockerfile_path, image_name)`
- Builds target container from Dockerfile
- Validates build success before proceeding
- Required before testing container changes

### `container_test(image_name, test_commands)`
- Runs test commands against specified container
- Executes from bootstrap container (external testing)
- Provides isolated test environment

## Review and Approval Process

### DevOps Story Completion Requirements

1. **Valid Target Container**: Must create/update a valid target container and call `container_update` tool
2. **Dockerfile Inclusion**: Current Dockerfile content included in approval request
3. **Build Validation**: Proof that target container builds successfully
4. **Test Results**: Evidence that container changes work as expected
5. **Change Documentation**: Clear explanation of what infrastructure changed

**Completion Gate**: DevOps stories cannot be marked complete unless `validTargetImage=true`. If not, the coder receives guidance to create a valid target container and run `container_update` tool.

### Architect Review Checklist

- [ ] Dockerfile changes align with story requirements
- [ ] No ad-hoc installations or environment pollution
- [ ] Container builds successfully
- [ ] Tests validate the specific changes made
- [ ] Changes don't break existing functionality
- [ ] Security and best practices followed

## Container Staleness Mitigation

### The Problem
Dockerfile may be updated but Docker image not rebuilt, leading to:
- Tests running against stale image
- Inconsistent environment assumptions
- Hidden configuration drift

### Mitigation Strategies

1. **Forced Rebuilds**: Testing phase always rebuilds before testing
2. **Dockerfile Review**: Architect reviews actual Dockerfile content in approval
3. **Trust-and-Verify**: Allow future stories to catch and fix inconsistencies
4. **Build Validation**: Require proof of successful container build

## Best Practices

### For DevOps Stories

1. **Document Container State**: Always explain what container changes are made
2. **Test Thoroughly**: Validate changes work in clean target container
3. **Minimize Changes**: Make focused, incremental infrastructure changes
4. **Dockerfile First**: Update Dockerfile before making any container modifications
5. **Clean Transitions**: Use container_switch() appropriately for testing

### For Architects

1. **Review Dockerfile**: Always examine Dockerfile changes in approval requests
2. **Check Dependencies**: Ensure DevOps stories properly block dependent app work
3. **Validate Tests**: Confirm tests actually validate the infrastructure changes
4. **Security Review**: Check for security implications of container changes

### For System Design

1. **Separation of Concerns**: Keep bootstrap and target containers distinct
2. **Tool Boundaries**: Use container tools for all environment transitions
3. **Configuration Management**: All persistent config in version-controlled files
4. **Rollback Planning**: Ensure changes can be reverted through Dockerfile

## Implementation Status

- [x] Two-container model established
- [x] Bootstrap container (maestro-bootstrap) available
- [x] Target container configuration in project config
- [x] Container-aware DevOps prompts
- [x] Container switching tool implementation
- [x] Dockerfile inclusion in approval process
- [x] Updated story generation templates
- [ ] Container validation logic in config
- [ ] Story validation before database persistence
- [ ] DevOps completion gate with validTargetImage check

---

*This approach balances flexibility with safety, allowing multiple DevOps stories while maintaining environment integrity through proper tooling and constraints.*