// Package readiness is the provider-neutral vocabulary for a data plane that
// cannot be used yet (Phase 3 item 3, design D5/D6).
//
// It exists because the Orchestrator sees store.Store and nothing below it,
// and every sentinel that describes a not-ready LOCAL plane lives in `stack`
// — the local composer, which the Orchestrator must not import. So the
// Orchestrator cannot classify a startup failure by errors.Is against its
// producer. This package is what crosses the boundary instead: a cause, a
// detail, and the remedy an operator applies. Marker knowledge stays below.
//
// It is its own package rather than part of `store` because a readiness
// failure exists BEFORE a Store does.
//
// Producers map onto it explicitly — `stack` for the local marker and key
// states, `plane` for what the probe observes, `cloud` for its own — and
// each mapping is proved by a behavioural test that puts a plane into the
// state and asserts the cause AND the remedy. Nothing here derives a cause
// from anything.
package readiness

import (
	"errors"
	"fmt"
	"slices"
)

// Cause names why a plane cannot be used. One per enumerated not-ready
// state; the enumeration is the design's, and a fixed count is not.
type Cause string

// The enumerated causes.
const (
	// NoPlane: nothing has ever been provisioned where the plane was expected.
	NoPlane Cause = "no_plane"
	// RootKeyMissing: a provisioned plane whose root-of-trust key is absent.
	// Distinct from NoPlane because the remedies are opposite — restoring a
	// key that never existed sends the operator looking for nothing.
	RootKeyMissing Cause = "root_key_missing"
	// RestoreIncomplete: a restore began deleting into the data root and did
	// not finish. Never start.
	RestoreIncomplete Cause = "restore_incomplete"
	// RestoreUnverified: a restore completed but its verification debt is
	// unsettled. Ordinary use is refused; the lifecycle `up` settles it.
	RestoreUnverified Cause = "restore_unverified"
	// RecoveryInterrupted: a new-key recovery was interrupted; an orphaned
	// server may still own the data directory.
	RecoveryInterrupted Cause = "recovery_interrupted"
	// ObjectStoreUnusable: the object store did not answer, or the bucket is
	// absent or unreadable.
	ObjectStoreUnusable Cause = "object_store_unusable"
	// Unreachable: the database did not accept a connection.
	Unreachable Cause = "unreachable"
	// SchemaUnreadable: a connection was made and the schema version could
	// not be read for a reason other than "never migrated".
	SchemaUnreadable Cause = "schema_unreadable"
	// SchemaBehind: the plane's schema is older than this binary's,
	// including version 0 — a cluster nobody has migrated.
	SchemaBehind Cause = "schema_behind"
	// SchemaDirty: a migration failed partway and its version is marked dirty.
	SchemaDirty Cause = "schema_dirty"
	// SchemaAhead: the plane's schema is newer than this binary's.
	SchemaAhead Cause = "schema_ahead"
)

// Causes is every cause this package defines, for tests that must be
// exhaustive.
//
//nolint:gochecknoglobals // Immutable enumeration.
var Causes = []Cause{
	NoPlane, RootKeyMissing, RestoreIncomplete, RestoreUnverified, RecoveryInterrupted,
	ObjectStoreUnusable, Unreachable, SchemaUnreadable, SchemaBehind, SchemaDirty, SchemaAhead,
}

// Known reports whether c is one of the enumerated causes.
func (c Cause) Known() bool { return slices.Contains(Causes, c) }

// Error is a refusal to use the plane, carrying what an operator needs.
//
// Detail says what was observed; Remedy says what to do; Err is the
// producer's own error, kept in the chain so a caller that does know the
// producer can still errors.Is against it.
type Error struct {
	Err    error
	Cause  Cause
	Detail string
	Remedy string
}

// Error renders cause, detail and remedy, then the underlying error.
func (e *Error) Error() string {
	msg := fmt.Sprintf("data plane not ready (%s): %s. Remedy: %s", e.Cause, e.Detail, e.Remedy)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap exposes the producer's error.
func (e *Error) Unwrap() error { return e.Err }

// Refuse builds a readiness error. A cause outside the enumeration, or an
// empty remedy, is a producer defect and panics: a refusal that cannot say
// what to do is the failure this package exists to prevent, and it should
// fail where the mapping is written rather than where an operator reads it.
func Refuse(cause Cause, detail, remedy string, err error) error {
	if !cause.Known() {
		panic(fmt.Sprintf("readiness: unknown cause %q", cause))
	}
	if remedy == "" {
		panic(fmt.Sprintf("readiness: cause %s refused with no remedy", cause))
	}
	return &Error{Cause: cause, Detail: detail, Remedy: remedy, Err: err}
}

// CauseOf reports the cause carried anywhere in err's chain.
func CauseOf(err error) (Cause, bool) {
	var r *Error
	if errors.As(err, &r) {
		return r.Cause, true
	}
	return "", false
}

// RemedyOf reports the remedy carried anywhere in err's chain.
func RemedyOf(err error) (string, bool) {
	var r *Error
	if errors.As(err, &r) {
		return r.Remedy, true
	}
	return "", false
}

// WithRemedy returns err with its remedy replaced, for a composer that
// knows the deployment-specific command where the producer knew only the
// neutral action. An error carrying no readiness cause is returned as is.
//
// A new error is returned rather than the found one mutated: the original
// may be shared, and rewriting it would change what an earlier holder sees.
//
// The result carries the PRODUCER's diagnostic, not the error it was handed.
// Handed `err` it would carry a chain that already contains the readiness
// error, and rendering walks that chain -- so the operator read the whole
// refusal twice, with the SUPERSEDED remedy last, which is the one thing a
// re-remedy exists to prevent. Keeping `r.Err` renders once and still lets a
// caller errors.Is the producer's own sentinel.
//
// The cost is that a composer's wrapping text between the producer and here
// is not rendered. That is deliberate: a composer with context to add puts it
// in the Detail, which is the field the operator reads, rather than in a
// wrapper this function would have to unpick.
func WithRemedy(err error, remedy string) error {
	var r *Error
	if !errors.As(err, &r) {
		return err
	}
	if remedy == "" {
		panic(fmt.Sprintf("readiness: cause %s re-remedied with an empty remedy", r.Cause))
	}
	return &Error{Cause: r.Cause, Detail: r.Detail, Remedy: remedy, Err: r.Err}
}
