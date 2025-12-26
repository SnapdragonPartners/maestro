package github

import (
	"orchestrator/pkg/forge"
)

// init registers the GitHub client factory with the forge package.
func init() {
	forge.RegisterGitHubClientFactory(newClientFromFactory)
}

// newClientFromFactory creates a GitHub forge client.
// The projectDir parameter is not used for GitHub (only for Gitea state loading).
func newClientFromFactory() (forge.Client, error) {
	return NewClientFromConfig()
}
