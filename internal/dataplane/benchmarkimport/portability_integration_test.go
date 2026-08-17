//go:build integration

package benchmarkimport_test

import (
	"testing"

	"orchestrator/internal/dataplane/importslice"
)

// TestImportSliceAgainstTheLocalPlane runs the shared portability slice against
// the LOCAL Docker composition.
//
// It is one half of #286's first acceptance criterion — the identical
// benchmark-import vertical slice passing against local Docker and one managed
// cloud configuration. The other half calls the SAME function against Cloud SQL
// and Cloud Storage, in internal/dataplane/cloud.
//
// Its job here is to be the CONTROL. A cloud result means very little on its own:
// a failure could as easily be a wrong slice as a divergent composition, and a
// pass could be a slice that asserts nothing. Running the same code against the
// local plane, where every other importer test already holds, is what makes a
// cloud verdict attributable to the composition.
//
// It does not replace this package's own suite, which pins the importer's
// behaviour in detail. Nothing here is a new claim about the importer.
//
// No object purge, unlike the cloud caller: planetest gives each test its own
// bucket and removes it, so the database and the object store are disposed
// together. The managed plane is where that stops being true.
func TestImportSliceAgainstTheLocalPlane(t *testing.T) {
	importslice.Run(t, newPlane(t).store)
}
