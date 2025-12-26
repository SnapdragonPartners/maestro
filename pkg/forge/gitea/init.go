package gitea

import (
	"orchestrator/pkg/forge"
)

// init registers the Gitea client factory with the forge package.
func init() {
	forge.RegisterGiteaClientFactory(newClientFromState)
}

// newClientFromState creates a Gitea client from runtime state.
func newClientFromState(projectDir string) (forge.Client, error) {
	state, err := forge.LoadState(projectDir)
	if err != nil {
		return nil, err
	}

	return NewClient(
		state.URL,
		state.Token,
		state.Owner,
		state.RepoName,
	), nil
}
