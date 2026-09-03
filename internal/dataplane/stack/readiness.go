package stack

import (
	"errors"
	"fmt"

	"orchestrator/internal/dataplane/readiness"
)

// The local commands an operator runs. Named once, so a remedy and the
// Makefile target it points at cannot drift apart silently.
const (
	remedyUp         = "run `make dataplane-up`, the only operation that provisions a plane"
	remedyRestoreKey = "restore the root-of-trust key file beside the backup it came from, or run `make dataplane-recover-key` for new-key recovery"
	remedyFinish     = "re-run `make dataplane-restore` from a good archive to finish it, or `make dataplane-reset FORCE=1` to discard the plane"
	remedyVerify     = "run `make dataplane-up`, which starts the plane specifically to verify it and settles the debt; or `make dataplane-reset FORCE=1` to discard it"
	remedyRecover    = "finish the recovery with `make dataplane-recover-key`; a recovery container may still hold the data directory. `make dataplane-down` keeps it resumable; `make dataplane-reset FORCE=1` discards it"
	remedyStart      = "start the plane with `make dataplane-up` and check the service"
	remedyMigrate    = "run `make dataplane-migrate`"
	remedyRepair     = "repair the failed migration by hand, then `dataplanectl force-version`; docs/v2/phase_2/design_schema_core.md describes the judgement involved"
	remedyNewer      = "run a newer Maestro binary against this plane; never downgrade the plane to match an old one"
)

// localCause maps one of this package's sentinels to the neutral cause and
// the local remedy.
type localCause struct {
	sentinel error
	name     string
	cause    readiness.Cause
	remedy   string
}

// localCauses is the explicit mapping for every sentinel that can reach a
// caller of OpenSeam under ordinary use (design D6).
//
// Explicit, not derived: an AST can enumerate names, but it cannot say which
// of them reach the use path or what an operator should do about them. The
// structural test beside this file checks the OTHER direction — that every
// exported sentinel in the package is either here or in notOnUsePath with a
// reason — so one added later cannot go unclassified.
//
//nolint:gochecknoglobals // Immutable mapping table.
var localCauses = []localCause{
	{sentinel: ErrNoPlane, name: "ErrNoPlane", cause: readiness.NoPlane, remedy: remedyUp},
	{sentinel: ErrPlaneLocked, name: "ErrPlaneLocked", cause: readiness.RootKeyMissing, remedy: remedyRestoreKey},
	{sentinel: ErrRestoreIncomplete, name: "ErrRestoreIncomplete", cause: readiness.RestoreIncomplete, remedy: remedyFinish},
	{sentinel: ErrRestoreUnverifiedPending, name: "ErrRestoreUnverifiedPending", cause: readiness.RestoreUnverified, remedy: remedyVerify},
	{sentinel: ErrRecoveryInterrupted, name: "ErrRecoveryInterrupted", cause: readiness.RecoveryInterrupted, remedy: remedyRecover},
}

// notOnUsePath is every exported sentinel that cannot reach OpenSeam, each
// with the reason. Being listed is a decision, not an omission: an entry
// point absent from BOTH tables fails the structural test.
//
//nolint:gochecknoglobals // Immutable expectation table.
var notOnUsePath = map[string]string{
	"ErrNotReady":                     "produced by up's readiness wait, a lifecycle verb; OpenSeam does not wait",
	"ErrInvalidProjectName":           "a configuration error at NewConfig, before any operation",
	"ErrArchiveIncomplete":            "backup and restore only",
	"ErrDestinationExists":            "backup only",
	"ErrUnsupportedFileType":          "the data-root copier, used by backup and restore",
	"ErrPathOverlap":                  "the data-root copier, used by backup and restore",
	"ErrRecoveryNotAuthorized":        "recover-key only",
	"ErrRecoveryIncoherent":           "readRecoveryMarker, reached only by recover-key; the use-path guard stats the marker and never reads it",
	"ErrRecoveryForeignMarker":        "readRecoveryMarker, as above",
	"ErrPopulatedRoot":                "restore only",
	"ErrArchiveMissingService":        "restore only",
	"ErrRestoreUnverified":            "the verification pass inside restore and up; ordinary use sees the PENDING marker, ErrRestoreUnverifiedPending",
	"ErrArchiveCarriesLifecycleState": "restore only",
}

// classifyLocal maps a guard or key failure to its readiness cause. An error
// matching no sentinel is returned unchanged: it is a defect or an I/O
// failure, not a plane state, and inventing a cause for it would tell the
// operator to do something that will not help.
func classifyLocal(err error) error {
	for i := range localCauses {
		entry := &localCauses[i]
		if errors.Is(err, entry.sentinel) {
			return readiness.Refuse(entry.cause, err.Error(), entry.remedy, err)
		}
	}
	return err
}

// localRemedies replaces the neutral remedies the probe attaches with the
// commands this deployment actually has.
//
//nolint:gochecknoglobals // Immutable mapping table.
var localRemedies = map[readiness.Cause]string{
	readiness.Unreachable:      remedyStart,
	readiness.SchemaUnreadable: remedyStart + "; if it is running, inspect its schema_migrations table",
	readiness.SchemaBehind:     remedyMigrate,
	readiness.SchemaDirty:      remedyRepair,
	readiness.SchemaAhead:      remedyNewer,
}

// localizeProbe rewrites a probe refusal's remedy for the local plane.
func localizeProbe(err error) error {
	cause, ok := readiness.CauseOf(err)
	if !ok {
		return err
	}
	remedy, known := localRemedies[cause]
	if !known {
		return err
	}
	return readiness.WithRemedy(err, remedy)
}

// objectStoreUnusable is the bucket half of the local mapping.
func objectStoreUnusable(err error) error {
	return readiness.Refuse(readiness.ObjectStoreUnusable,
		fmt.Sprintf("the object store could not be provisioned or reached: %v", err), remedyStart, err)
}
