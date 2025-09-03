# Container Tools

This directory contains the container management tools used by Maestro agents.

## File Structure

The container tools have been split from a single 1022-line `container_tools.go` file into organized, focused files:

- `container_common.go` - Shared constants and utilities
- `container_build.go` - Container building tool ✅ **EXTRACTED**
- `container_update.go` - Container configuration and pinned image management ✅ **EXTRACTED**  
- `container_test_tool.go` - Container testing tool ✅ **EXTRACTED**
- `container_list.go` - Container listing tool ✅ **EXTRACTED**
- `container_switch.go` - Container switching tool ✅ **EXTRACTED**

## Tool Overview

### Container Build (`container_build`)
- Builds Docker containers from Dockerfile
- Supports buildx and fallback to legacy docker build
- Validates containers after building

### Container Update (`container_update`)
- Two modes: Container config update and pinned image management
- Atomically sets pinned target image ID (Story 5 from promotion plan)
- Updates container configuration

### Container Test (`container_test`)
- Runs validation in temporary containers
- Mount policy: CODING=RW, others=RO  
- Host execution (not docker-in-docker)

### Container Switch (`container_switch`)
- Switches agent execution environment
- Falls back to bootstrap container on failure
- Updates agent configuration

### Container List (`container_list`)
- Lists available containers with registry status
- Shows running/stopped containers

## Migration Status

**COMPLETED**: Successfully split the 1022-line container_tools.go file into focused, organized files. All container tools have been extracted into separate files for improved maintainability and navigation.

**Next Steps**: 
1. Verify all imports are correctly working in the new structure
2. Update any references to the old file structure  
3. Remove or deprecate the original container_tools.go file
4. Run tests to ensure functionality is preserved