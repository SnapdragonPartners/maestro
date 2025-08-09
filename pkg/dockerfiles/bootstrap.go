// Package dockerfiles provides embedded Dockerfile content for various container types.
package dockerfiles

import _ "embed"

//go:embed bootstrap.dockerfile
var bootstrapDockerfile string

// GetBootstrapDockerfile returns the embedded bootstrap Dockerfile content.
func GetBootstrapDockerfile() string {
	return bootstrapDockerfile
}
