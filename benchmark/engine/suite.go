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

// bundleAccount tracks conservative suite-budget admission for one config in
// its budget dimension — USD for hosted configs, tokens for local (item 5.1).
// Admission reserves the attempt's *effective per-run maximum* — the most an
// admitted attempt may legitimately spend — so the suite cannot overshoot its
// cap by launching (Codex round 2). After the attempt, settle adjusts the
// charge to the observed amount when known; unknown keeps the full
// reservation, never zero.
type bundleAccount struct {
	config    string
	dimension string // results.DimensionUSD | results.DimensionTokens
	cap       float64
	charged   float64
	observed  float64
}

func (b *bundleAccount) admit(reserve float64) bool {
	if b.charged+reserve > b.cap {
		return false
	}
	b.charged += reserve
	return true
}

func (b *bundleAccount) settle(reserve, observed float64, known bool) {
	if !known {
		return // conservative: the reservation stands
	}
	// Replace the reservation with reality — including observed above the
	// reservation (a post-hoc target that overspent really did).
	b.charged += observed - reserve
	b.observed += observed
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
	run.syncAccounts() // the initial manifest must already carry the accounts
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
					// Fatal engine/persistence error: the manifest must
					// still say what happened — best-effort finalize as
					// interrupted before surfacing the error.
					_, _ = run.finish(results.StopInterrupted) //nolint:errcheck // best effort; the primary error is surfaced below
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
		acct := &bundleAccount{config: bundle.Bundle.Name, dimension: results.DimensionUSD, cap: bundle.Bundle.Budget.MaxCostUSDPerSuite}
		if bundle.Bundle.Local {
			acct.dimension = results.DimensionTokens
			acct.cap = float64(bundle.Bundle.Budget.MaxTokensPerSuite)
		}
		run.accounts[bundle.Bundle.Name] = acct
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
	// Reserve what an admitted attempt may legitimately spend: its effective
	// per-run maximum in this config's budget dimension (cost for hosted,
	// tokens for local), not the (smaller) expectation.
	eff := effectiveBudget(st.Definition.Budget, bundle.Bundle)
	reserve := eff.MaxCostUSD
	if bundle.Bundle.Local {
		reserve = float64(eff.MaxTokens)
	}
	if !account.admit(reserve) {
		s.budgetHit = true
		entry.Status = results.AttemptSkipped
		entry.Reason = fmt.Sprintf("suite budget: charged %.0f + per-run cap %.0f exceeds cap %.0f (%s)", account.charged, reserve, account.cap, account.dimension)
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
	// Settle against the observed amount in the config's dimension.
	metric := runrecord.MetricCostUSD
	if bundle.Bundle.Local {
		metric = runrecord.MetricTokensTotal
	}
	observed, known := rec.Metrics[metric].Float64()
	account.settle(reserve, observed, known)
	return false, s.persist()
}

// syncAccounts refreshes the manifest's per-config budget accounts from the
// live accounts, in stable bundle order (not map order). Called before the
// initial write and on every persist so the on-disk manifest always carries
// complete accounts.
func (s *suiteRun) syncAccounts() {
	accounts := make([]results.BudgetAccount, 0, len(s.accounts))
	for _, bundle := range s.params.Bundles {
		a := s.accounts[bundle.Bundle.Name]
		accounts = append(accounts, results.BudgetAccount{
			Config:    a.config,
			Dimension: a.dimension,
			Cap:       a.cap,
			Charged:   a.charged,
			Observed:  a.observed,
		})
	}
	s.manifest.BudgetAccounts = accounts
}

// persist refreshes the accounts and writes the manifest.
func (s *suiteRun) persist() error {
	s.syncAccounts()
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
