package main

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/orchestrator"
	"orchestrator/pkg/version"
)

// orchestratorOpener is the composition root's half of design D3: the only
// place that names the LOCAL composer on the Orchestrator's behalf. The
// Orchestrator receives an Opener and never sees stack.Config.
//
// The registries are the Orchestrator's own -- the work types and its
// (empty) key vocabulary -- not the benchmark verbs', and not a union.
func orchestratorOpener(cfg *stack.Config) (orchestrator.Opener, error) {
	types, err := orchestrator.Registry()
	if err != nil {
		return nil, fmt.Errorf("compose the orchestrator's seam: %w", err)
	}
	running, err := runningHarness()
	if err != nil {
		return nil, fmt.Errorf("compose the orchestrator's seam: %w", err)
	}
	caller := plane.Caller{
		Types:   types,
		Keys:    orchestrator.Keys(),
		Prompts: orchestrator.Prompts(),
		Harness: running,
	}
	return func(ctx context.Context) (store.Store, error) {
		return stack.OpenSeam(ctx, cfg, caller)
	}, nil
}

// runningHarness is this binary's version, validated (item 4 design, D3 and
// D8). Every verb that opens a seam crosses it, and it runs BEFORE the plane
// is touched: a mis-stamped build is refused here, typed, rather than opening
// a seam and recording a version no declared range can be compared with.
//
// pkg/version is read here and nowhere below: the composition root is the one
// place that knows it is a binary.
func runningHarness() (harness.Version, error) {
	running, err := harness.Parse(version.Version)
	if err != nil {
		return harness.Version{}, fmt.Errorf("this binary's version stamp is unusable, so it may not open a data plane: %w", err)
	}
	return running, nil
}

// runRecover starts the Orchestrator against the local plane and prints
// its recovery projection: every open dispatch and the class it landed in.
// It changes nothing; the plane wins.
func runRecover(ctx context.Context, cfg *stack.Config, opts *runOptions) error {
	switch {
	case opts.org == "":
		return errors.New("recover needs -org <slug>")
	case opts.user == "":
		return errors.New("recover needs -user <handle>")
	}
	open, err := orchestratorOpener(cfg)
	if err != nil {
		return err
	}
	o, err := orchestrator.Start(ctx, open, orchestrator.Config{OrganizationSlug: opts.org, OperatorHandle: opts.user})
	if err != nil {
		// A StartupRefused renders its own cause, observation and remedy;
		// the prefix names the verb and paraphrases nothing.
		return fmt.Errorf("recover: %w", err)
	}
	defer o.Close()

	projection := o.Projection()
	classes := make([]string, 0, len(projection.Counts))
	for class := range projection.Counts {
		classes = append(classes, string(class))
	}
	sort.Strings(classes)
	fmt.Printf("organization %s, operator %s: %d open dispatch(es)\n", o.Organization().Slug, o.Operator().Handle, len(projection.Rows))
	for _, class := range classes {
		fmt.Printf("  %-28s %d\n", class, projection.Counts[orchestrator.Class(class)])
	}
	for i := range projection.Rows {
		row := &projection.Rows[i]
		if row.Divergence != nil {
			fmt.Printf("  dispatch %s story %s: %s -- %s\n", row.DispatchID, row.StoryID, row.Class, row.Divergence)
		}
	}
	return nil
}
