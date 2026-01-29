// Package workspace provides workspace verification and validation functionality.
package workspace

// BootstrapRequirementID is a typed identifier for bootstrap requirements.
// These map to BootstrapFailureType values but represent "what's needed" rather than "what failed".
// Using a string-based type allows JSON serialization while providing type safety.
type BootstrapRequirementID string

const (
	// BootstrapReqContainer indicates a valid project container is needed.
	BootstrapReqContainer BootstrapRequirementID = "container"
	// BootstrapReqDockerfile indicates a Dockerfile needs to be created.
	BootstrapReqDockerfile BootstrapRequirementID = "dockerfile"
	// BootstrapReqBuildSystem indicates Makefile/build targets need setup.
	BootstrapReqBuildSystem BootstrapRequirementID = "build_system"
	// BootstrapReqKnowledgeGraph indicates .maestro/knowledge.dot needs to be created.
	BootstrapReqKnowledgeGraph BootstrapRequirementID = "knowledge_graph"
	// BootstrapReqGitAccess indicates git repository access needs to be configured.
	BootstrapReqGitAccess BootstrapRequirementID = "git_access"
	// BootstrapReqBinarySize indicates large files need Git LFS setup.
	BootstrapReqBinarySize BootstrapRequirementID = "binary_size"
	// BootstrapReqExternalTools indicates required external tools are missing.
	BootstrapReqExternalTools BootstrapRequirementID = "external_tools"
)

// ValidBootstrapRequirements is the set of valid requirement IDs.
//
//nolint:gochecknoglobals // Intentional: static lookup table for validation
var ValidBootstrapRequirements = map[BootstrapRequirementID]bool{
	BootstrapReqContainer:      true,
	BootstrapReqDockerfile:     true,
	BootstrapReqBuildSystem:    true,
	BootstrapReqKnowledgeGraph: true,
	BootstrapReqGitAccess:      true,
	BootstrapReqBinarySize:     true,
	BootstrapReqExternalTools:  true,
}

// IsValidRequirementID checks if a requirement ID is valid.
func IsValidRequirementID(id BootstrapRequirementID) bool {
	return ValidBootstrapRequirements[id]
}

// RequirementIDToFailureType converts a BootstrapRequirementID to its corresponding BootstrapFailureType.
func RequirementIDToFailureType(id BootstrapRequirementID) BootstrapFailureType {
	switch id {
	case BootstrapReqContainer, BootstrapReqDockerfile:
		return BootstrapFailureContainer
	case BootstrapReqBuildSystem:
		return BootstrapFailureBuildSystem
	case BootstrapReqKnowledgeGraph:
		return BootstrapFailureInfrastructure
	case BootstrapReqGitAccess:
		return BootstrapFailureGitAccess
	case BootstrapReqBinarySize:
		return BootstrapFailureBinarySize
	case BootstrapReqExternalTools:
		return BootstrapFailureExternalTools
	default:
		return BootstrapFailureInfrastructure
	}
}

// requirementDescriptions provides human-readable descriptions for each requirement.
//
//nolint:gochecknoglobals // Intentional: static lookup table for descriptions
var requirementDescriptions = map[BootstrapRequirementID]string{
	BootstrapReqContainer:      "Project container needs to be configured",
	BootstrapReqDockerfile:     "Dockerfile needs to be created in .maestro/ directory",
	BootstrapReqBuildSystem:    "Makefile with build/test/lint/run targets and .gitignore with project-appropriate entries need to be created",
	BootstrapReqKnowledgeGraph: "Knowledge graph (.maestro/knowledge.dot) needs to be created",
	BootstrapReqGitAccess:      "Git repository access needs to be configured",
	BootstrapReqBinarySize:     "Large files need Git LFS setup",
	BootstrapReqExternalTools:  "Required external tools need to be installed",
}

// requirementPriorities defines the priority for fixing each requirement (1=highest).
//
//nolint:gochecknoglobals // Intentional: static lookup table for priorities
var requirementPriorities = map[BootstrapRequirementID]int{
	BootstrapReqContainer:      1, // Container is critical - needed to run anything
	BootstrapReqDockerfile:     1, // Dockerfile is critical - needed to build container
	BootstrapReqBuildSystem:    1, // Build system is critical - needed to build/test
	BootstrapReqGitAccess:      1, // Git access is critical - needed for worktrees
	BootstrapReqBinarySize:     1, // Binary size is critical - blocks pushes
	BootstrapReqKnowledgeGraph: 2, // Knowledge graph is high priority but not blocking
	BootstrapReqExternalTools:  2, // External tools are high priority but not blocking
}

// RequirementIDsToFailures converts a slice of requirement IDs to BootstrapFailure structs.
// This is used by the architect to convert structured requirements into the format
// expected by the bootstrap template renderer.
func RequirementIDsToFailures(ids []BootstrapRequirementID) []BootstrapFailure {
	failures := make([]BootstrapFailure, 0, len(ids))
	for _, id := range ids {
		if !IsValidRequirementID(id) {
			continue
		}
		failure := BootstrapFailure{
			Type:        RequirementIDToFailureType(id),
			Component:   string(id),
			Description: requirementDescriptions[id],
			Priority:    requirementPriorities[id],
			Details:     make(map[string]string),
		}
		failures = append(failures, failure)
	}
	return failures
}
