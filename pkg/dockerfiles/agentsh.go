package dockerfiles

import _ "embed"

//go:embed agentsh_config.yaml
var agentshServerConfig string

//go:embed agentsh_entrypoint.sh
var agentshEntrypoint string

//go:embed agentsh_default_policy.yaml
var agentshDefaultPolicy string

// GetAgentshServerConfig returns the embedded agentsh server config YAML.
func GetAgentshServerConfig() string {
	return agentshServerConfig
}

// GetAgentshEntrypoint returns the embedded agentsh entrypoint script.
func GetAgentshEntrypoint() string {
	return agentshEntrypoint
}

// GetAgentshDefaultPolicy returns the embedded maestro-specific agentsh policy YAML.
// NOTE: This policy uses a maestro-internal schema (rules.commands/network/files) that
// does not yet match the agentsh v0.10.x schema (command_rules/network_rules/file_rules).
// It is copied into the image as a placeholder; activation requires schema alignment.
func GetAgentshDefaultPolicy() string {
	return agentshDefaultPolicy
}
