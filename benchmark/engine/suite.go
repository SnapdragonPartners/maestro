package engine

import (
	"context"
	"fmt"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/results"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// SuiteParams describes one suite run: the full matrix is
// stories × bundles × repeats.
type SuiteParams struct {
	SuiteRunID string
	Stories    []*story.Loaded
	Bundles    []*mph.Loaded
	Repeats    int
}

// bundleAccount tracks conservative suite-budget admission for one bundle:
// an attempt is charged its declared expected cost up front, so the suite
// cannot overshoot by launching (design_engine.md). Observed cost above
// the expectation tops the charge up; unavailable/unsupported cost stays
// charged at the expectation, never zero.
type bundleAccount struct {
	capUSD     float64
	chargedUSD float64
}

func (b *bundleAccount) admit(expected float64) bool {
	if b.chargedUSD+expected > b.capUSD {
		return false
	}
	b.chargedUSD += expected
	return true
}

func (b *bundleAccount) topUp(expected, observed float64, known bool) {
	if known && observed > expected {
		b.chargedUSD += observed - expected
	}
}

// suiteRun is the in-flight state of one suite execution.
type suiteRun struct {
	engine    *Engine
	manifest  *results.Manifest
	accounts  map[string]*bundleAccount
	params    SuiteParams
	budgetHit bool
}

// RunSuite executes the matrix sequentially with per-attempt isolation,
// persisting the manifest after every state change so a partial suite is
// always distinguishable from a corrupt results file.
func (e *Engine) RunSuite(ctx context.Context, p SuiteParams) (*results.Manifest, error) {
	if p.Repeats < 1 {
		return nil, fmt.Errorf("repeats must be at least 1, got %d", p.Repeats)
	}
	if len(p.Stories) == 0 || len(p.Bundles) == 0 {
		return nil, fmt.Errorf("suite needs at least one story and one bundle")
	}
	run := e.planSuite(p)
	if err := e.Store.WriteManifest(run.manifest); err != nil {
		return run.manifest, fmt.Errorf("write manifest: %w", err)
	}
	idx := 0
	for _, bundle := range p.Bundles {
		for _, st := range p.Stories {
			for repeat := 1; repeat <= p.Repeats; repeat++ {
				stop, err := run.cell(ctx, &run.manifest.Attempts[idx], st, bundle, repeat)
				idx++
				if err != nil {
					return run.manifest, err
				}
				if stop {
					return run.finish(results.StopInterrupted)
				}
			}
		}
	}
	stopReason := results.StopCompleted
	if run.budgetHit {
		stopReason = results.StopSuiteBudgetExhausted
	}
	return run.finish(stopReason)
}

// planSuite builds the planned manifest and per-bundle budget accounts.
func (e *Engine) planSuite(p SuiteParams) *suiteRun {
	run := &suiteRun{
		engine:   e,
		params:   p,
		manifest: &results.Manifest{SuiteRunID: p.SuiteRunID, StopReason: results.StopRunning},
		accounts: make(map[string]*bundleAccount, len(p.Bundles)),
	}
	for _, bundle := range p.Bundles {
		run.accounts[bundle.Bundle.Name] = &bundleAccount{capUSD: bundle.Bundle.Budget.MaxCostUSDPerSuite}
		run.manifest.CapUSD += bundle.Bundle.Budget.MaxCostUSDPerSuite
		for _, st := range p.Stories {
			for repeat := 1; repeat <= p.Repeats; repeat++ {
				run.manifest.Attempts = append(run.manifest.Attempts, results.ManifestAttempt{
					Story:  st.Definition.ID,
					Config: bundle.Bundle.Name,
					Repeat: repeat,
					Status: results.AttemptPlanned,
				})
			}
		}
	}
	return run
}

// cell executes one matrix cell: admission, attempt, accounting, and
// manifest persistence. stop is true when the context is done.
func (s *suiteRun) cell(ctx context.Context, entry *results.ManifestAttempt, st *story.Loaded, bundle *mph.Loaded, repeat int) (bool, error) {
	if ctx.Err() != nil {
		return true, nil //nolint:nilerr // interruption is a manifest stop reason, not an engine error
	}
	account := s.accounts[bundle.Bundle.Name]
	expected := bundle.Bundle.Budget.ExpectedCostUSDPerRun
	if !account.admit(expected) {
		s.budgetHit = true
		entry.Status = results.AttemptSkipped
		entry.Reason = fmt.Sprintf("suite budget: charged %.2f + expected %.2f exceeds cap %.2f", account.chargedUSD, expected, account.capUSD)
		s.engine.logf("%s/%s r%d: skipped (%s)", st.Definition.ID, bundle.Bundle.Name, repeat, entry.Reason)
		return false, s.persist()
	}
	rec, err := s.engine.RunAttempt(ctx, st, bundle, s.params.SuiteRunID, repeat)
	if err != nil {
		entry.Status = results.AttemptSkipped
		entry.Reason = "engine error: " + err.Error()
		_ = s.persist() //nolint:errcheck // best effort before surfacing the real error
		return false, err
	}
	entry.Status = results.AttemptCompleted
	entry.RunID = rec.RunID
	observed, known := rec.Metrics[runrecord.MetricCostUSD].Float64()
	account.topUp(expected, observed, known)
	if known {
		s.manifest.ObservedUSD += observed
	}
	return false, s.persist()
}

// persist refreshes the manifest totals and writes it.
func (s *suiteRun) persist() error {
	total := 0.0
	for _, account := range s.accounts {
		total += account.chargedUSD
	}
	s.manifest.ChargedUSD = total
	if err := s.engine.Store.WriteManifest(s.manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// finish marks any remaining planned attempts skipped and persists the
// final manifest.
func (s *suiteRun) finish(stopReason string) (*results.Manifest, error) {
	for i := range s.manifest.Attempts {
		if s.manifest.Attempts[i].Status == results.AttemptPlanned {
			s.manifest.Attempts[i].Status = results.AttemptSkipped
			if s.manifest.Attempts[i].Reason == "" {
				s.manifest.Attempts[i].Reason = stopReason
			}
		}
	}
	s.manifest.StopReason = stopReason
	return s.manifest, s.persist()
}
