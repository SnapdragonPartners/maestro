package boundary_test

import (
	"context"
	"errors"

	"orchestrator/internal/boundary/family"
)

// The test-only families (design D3): a no-op with a secret slot, a family
// that fails after its commit point, and a family that never returns. They
// live under the boundary's tests and are never registered by a
// composition root; the mandatoriness guard (D2) counts them as their own
// tests. Commit 2 runs the registry, the vocabulary and substitution over
// them; commit 3's proofs drive them through Mediate.

// noopResolve is the target every test family resolves: keyed by Story, no
// shared resource, so two attempts under one execution both register (the
// "non-mutating family registers with no mutation key" row).
func noopResolve(execution family.Execution) (family.Target, error) {
	return family.Target{Key: "test/" + execution.StoryID.String()}, nil
}

// noopReconcile finds nothing: a no-op leaves no evidence.
func noopReconcile(context.Context, family.Attempt, family.Secrets) (family.Evidence, error) {
	return family.Evidence{}, nil
}

// noopFamily is a no-op with a secret slot: the substitution and no-token
// proofs run over it, and its effect echoes what it was given so the
// redaction pass has something to catch.
func noopFamily() family.Family {
	return family.Family{
		Kind: "test", Verb: "noop", Description: "does nothing, with a secret slot",
		Schema: family.Schema{Fields: []family.Field{
			{Name: "note", Type: family.String, Required: true, Classification: family.Persist},
			{Name: "hint", Type: family.String, Classification: family.DigestOnly},
			{Name: "count", Type: family.Integer, Classification: family.Persist},
			{Name: "ratio", Type: family.Number, Classification: family.Persist},
			{Name: "body", Type: family.String, Classification: family.Large},
			{Name: "token", Type: family.String, Classification: family.SecretSlot,
				Secret: &family.Slot{Name: "forge.token", Scope: family.ScopeRepository}},
		}},
		ResultSchema: family.Schema{Fields: []family.Field{
			{Name: "echo", Type: family.String, Classification: family.Persist},
		}},
		EffectSite:   family.OrchestratorSide,
		Checkability: "there is nothing to perform",
		CommitPoint:  "the return",
		Resolve:      noopResolve,
		Effect: func(_ context.Context, attempt family.Attempt, _ family.Secrets) (family.Result, error) {
			return family.Result{Values: map[string]any{"echo": attempt.Arguments["note"]}}, nil
		},
		Reconcile: noopReconcile,
	}
}

// errAfterCommit is what the failing family fails with.
var errAfterCommit = errors.New("the response was lost after transmission")

// failAfterCommitFamily passes its commit point and then fails, reporting
// that it did: the "every attempt is opened before its effect" and drainage
// proofs.
func failAfterCommitFamily() family.Family {
	f := noopFamily()
	f.Verb = "fail_after_commit"
	f.Description = "fails after its commit point"
	f.Effect = func(context.Context, family.Attempt, family.Secrets) (family.Result, error) {
		return family.Result{}, family.Fail(family.CommitPassed, errAfterCommit)
	}
	return f
}

// neverReturnsFamily blocks until its context ends: the in-progress,
// duplicate and stopped-call proofs.
func neverReturnsFamily() family.Family {
	f := noopFamily()
	f.Verb = "never_returns"
	f.Description = "blocks until cancelled"
	f.Effect = func(ctx context.Context, _ family.Attempt, _ family.Secrets) (family.Result, error) {
		<-ctx.Done()
		return family.Result{}, family.Fail(family.CommitUnknown, ctx.Err())
	}
	return f
}

// testFamilies is the three, in one call.
func testFamilies() []family.Family {
	return []family.Family{noopFamily(), failAfterCommitFamily(), neverReturnsFamily()}
}
