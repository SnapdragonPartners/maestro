// Package architect provides the architect agent implementation.
package architect

import (
	"fmt"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/specrender"
	"orchestrator/pkg/workspace"
)

// RenderBootstrapSpec renders the technical bootstrap specification from requirement IDs.
// This is a thin wrapper around specrender.RenderBootstrapSpec for backward compatibility.
func RenderBootstrapSpec(requirements []workspace.BootstrapRequirementID, logger *logx.Logger) (string, error) {
	spec, err := specrender.RenderBootstrapSpec(requirements, logger)
	if err != nil {
		return "", fmt.Errorf("render bootstrap spec: %w", err)
	}
	return spec, nil
}
