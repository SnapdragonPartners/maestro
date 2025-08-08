# Story Type Classification Implementation Plan

## Overview
Implement story type classification system to properly handle devops vs application development workflows. This addresses the container building story misinterpretation issue where infrastructure tasks were being treated as coding tasks.

## Key Principles
- **Two story types**: `devops` and `app`
- **Minimal devops scope**: DevOps stories should be scoped to infrastructure tasks ONLY (containers, deployment, configuration)
- **Default to app**: When in doubt, classify as app story - containers for app development are fully featured
- **Clean tool separation**: Consistent naming and clear responsibilities

## Implementation Stories

### Story 1: Add Story Type Classification to Data Models
**Priority**: High  
**Files**: `pkg/architect/spec2db.go`, `pkg/persistence/types.go`

- Add `StoryType` field to `Requirement` and `Story` structs
- Add classification logic in requirement parsing to detect devops vs app stories
- Modify `generateStoryContent()` to include story type in generated content
- Default all stories to "app" type unless explicitly infrastructure-focused

**Acceptance Criteria**:
- Story type field added to data models
- Classification logic detects devops keywords (docker, container, deployment, infrastructure)
- Generated story content includes story type
- Database schema updated for story_type column

### Story 2: Update Story Generation Template and Architect Prompts
**Priority**: High  
**Files**: `pkg/templates/story_generation.tpl.md`, architect prompts

- Add instruction to classify each story as "devops" or "app" 
- Include story type in JSON output format
- Emphasize infrastructure tasks vs application development separation
- Add guidance: "DevOps stories should be minimally scoped to infrastructure tasks ONLY. When in doubt, classify as app story."

**Acceptance Criteria**:
- Template includes story type classification instructions
- JSON output format includes story_type field
- Clear guidance on devops vs app classification
- Architect emphasizes minimal devops scope

### Story 3: Rename and Reorganize Container Tools
**Priority**: Medium  
**Files**: `pkg/tools/container_tools.go`

- Rename `UpdateContainerTool` → `ContainerUpdateTool`
- Create new `ContainerBuildTool` (focused on building containers)
- Create new `ContainerRunTool` (for running containers on host)
- Consistent naming: `container_build`, `container_update`, `container_run`
- Enforce config requirements in build tool wrapper

**Tool Responsibilities**:
- `container_build`: Build Docker containers from Dockerfile, enforce config requirements
- `container_update`: Update project configuration with container settings  
- `container_run`: Execute containers with proper host integration

**Acceptance Criteria**:
- All container tools renamed with consistent naming
- Each tool has clear, focused responsibility
- Config requirements enforced in appropriate tools
- Tool documentation updated

### Story 4: Create Build Tool Wrapper
**Priority**: Medium  
**Files**: `pkg/tools/build_tools.go`

- Wrap existing build service with story-type awareness
- Enforce proper build targets (make build, make test, make lint)
- Validate project has required Makefile targets before proceeding
- Provide clear error messages for missing targets

**Acceptance Criteria**:
- Build tool wrapper created
- Validates Makefile targets exist
- Story-type aware behavior
- Clear error messaging for missing dependencies

### Story 5: Implement Story-Kind-Aware TESTING State
**Priority**: High  
**Files**: `pkg/coder/driver.go`

- Modify `handleTesting()` to check story type from state data
- For "devops" stories: Skip traditional build/test, focus on container/infrastructure validation
- For "app" stories: Use existing build service flow with make build/test/lint
- Add story type to state data during task assignment

**Acceptance Criteria**:
- TESTING state checks story type
- Different testing flows for devops vs app stories
- DevOps testing focuses on infrastructure validation
- App testing uses traditional build/test/lint flow
- Story type propagated through state data

### Story 6: Update Testing Template with Conditional Logic
**Priority**: Medium  
**Files**: `pkg/templates/testing.tpl.md`

- Add conditional instructions based on story type
- Different tool recommendations for devops vs app stories
- Separate success criteria for infrastructure vs code changes
- Clear guidance on what constitutes successful testing for each type

**Template Structure**:
```markdown
{{if eq .StoryType "devops"}}
## DevOps Testing Instructions
Focus on infrastructure validation:
- container_build for Dockerfile changes
- container_run for testing execution
- Configuration validation
{{else}}
## Application Testing Instructions  
Focus on code validation:
- build tool for compilation
- test tool for unit/integration tests
- lint tool for code quality
{{end}}
```

**Acceptance Criteria**:
- Template has conditional logic based on story type
- Different instructions for devops vs app stories
- Clear success criteria for each story type
- Proper tool recommendations

## Implementation Notes

### Story Type Classification Rules
- **DevOps**: Infrastructure, containers, deployment, configuration, CI/CD
- **App**: Application code, features, business logic, algorithms, data processing
- **Default**: When uncertain, choose "app" - app containers are more fully featured

### Key Design Decisions
- No database migration needed (pre-release)
- Minimal devops scope principle
- Consistent tool naming convention
- Story-type awareness propagated through state data
- Template conditional logic for different workflows

### Success Metrics
- Container building stories correctly classified as devops
- DevOps stories use appropriate infrastructure tools
- App stories continue using traditional build/test workflows
- Clear separation of concerns between infrastructure and application tasks

## Future Enhancements

### Story 7: Safe Mode Container Fallback (Future)
**Priority**: Low (Future Enhancement)  
**Concept**: Bootstrap Container as Recovery Mode

The maestro-bootstrap container serves as a "safe mode" that agents can fall back to if the primary development container becomes corrupted, misconfigured, or broken during development.

**Use Cases**:
- Container build failures that break the development environment
- Dependency conflicts that make the app container unusable
- Configuration errors that prevent normal tooling from working
- Debugging container-related issues during app development

**Potential Implementation**:
- `safe_mode` tool that switches to maestro-bootstrap container
- Automatic fallback detection when tools fail repeatedly in app stories
- Manual recovery workflows for when agents get stuck
- Container health checking and automatic recovery

**Note**: This represents the bootstrap container's role as a minimal, reliable fallback environment that can always be used for diagnosis and recovery, even during app story development.