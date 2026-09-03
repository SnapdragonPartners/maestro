package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/dataplane/store"
)

// DefaultResultsDir mirrors the runner's own default, so the two halves of
// the pipeline agree about where records live without either being told.
const DefaultResultsDir = "benchmark/runs"

// suiteList collects a flag that may be repeated.
//
// Omitting it means every suite the store holds, which is a different
// request from naming them all: the store is the index, and a list written
// out by hand goes stale the next time the runner writes.
type suiteList []string

func (s *suiteList) String() string { return strings.Join(*s, ",") }

func (s *suiteList) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("a suite id cannot be blank")
	}
	*s = append(*s, trimmed)
	return nil
}

// openSeam opens the plane with the registry the benchmark verbs need.
//
// The registry is built HERE and handed in, because what types are readable
// is a property of this command's job rather than of the plane. An empty
// one — which the lifecycle verbs correctly use — would refuse every
// payload the importer came to write.
func openSeam(ctx context.Context, cfg *stack.Config) (store.Store, error) {
	types, err := registry.New(benchmarkimport.RegistryEntries())
	if err != nil {
		return nil, fmt.Errorf("build the benchmark artifact registry: %w", err)
	}
	// The benchmark verbs write no configuration, and say so. This registry
	// is THEIRS; the Orchestrator declares its own (design D7), and neither
	// quietly becomes the other's.
	seam, err := stack.OpenSeam(ctx, cfg, types, configkeys.MustNew(nil))
	if err != nil {
		return nil, fmt.Errorf("open the data plane: %w", err)
	}
	return seam, nil
}

// runBootstrap provisions an organization and a user.
//
// The smallest provisioning surface that makes the plane usable: nothing
// else creates either, and the importer resolves and never creates, because
// an import that silently provisions a tenant is a defect waiting for team
// mode (design D10).
func runBootstrap(ctx context.Context, cfg *stack.Config, opts *runOptions) error {
	switch {
	case opts.org == "":
		return errors.New("bootstrap needs -org <slug>")
	case opts.user == "":
		return errors.New("bootstrap needs -user <handle>")
	}
	// Display data defaults to the key, so the common case needs two flags
	// rather than four. It is still supplied EXPLICITLY to the seam: the
	// conflict rule compares stored display data against what was supplied,
	// and a default resolved here is what this command supplied.
	orgName := opts.orgName
	if orgName == "" {
		orgName = opts.org
	}
	userName := opts.userName
	if userName == "" {
		userName = opts.user
	}

	seam, err := openSeam(ctx, cfg)
	if err != nil {
		return err
	}
	defer seam.Close()

	organization, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: opts.org, DisplayName: orgName,
	})
	if err != nil {
		return fmt.Errorf("bootstrap organization %q: %w", opts.org, err)
	}
	fmt.Printf("%s organization %s (%s)\n", provisioned(organization.Created),
		organization.Record.Slug, organization.Record.DisplayName)

	user, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: opts.user, DisplayName: userName,
		OrganizationID: organization.Record.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("bootstrap user %q: %w", opts.user, err)
	}
	fmt.Printf("%s user %s (%s)\n", provisioned(user.Created),
		user.Record.Handle, user.Record.DisplayName)
	return nil
}

// provisioned distinguishes the two successful outcomes, which a conflict
// is neither of.
func provisioned(created bool) string {
	if created {
		return "created"
	}
	return "existing"
}

// runBenchmarkImport imports one or every suite in the results store.
func runBenchmarkImport(ctx context.Context, cfg *stack.Config, opts *runOptions) error {
	switch {
	case opts.org == "":
		return errors.New("benchmark import needs -org <slug>")
	case opts.operator == "":
		return errors.New("benchmark import needs -operator <handle>")
	}
	results := opts.results
	if results == "" {
		results = DefaultResultsDir
	}
	suites := []string(opts.suites)
	if len(suites) == 0 {
		found, err := benchmarkimport.ListSuites(results)
		if err != nil {
			return fmt.Errorf("list the suites in %s: %w", results, err)
		}
		if len(found) == 0 {
			return fmt.Errorf("no suites found in %s", results)
		}
		suites = found
	}

	seam, err := openSeam(ctx, cfg)
	if err != nil {
		return err
	}
	defer seam.Close()

	importer := benchmarkimport.New(seam)
	for _, suite := range suites {
		result, err := importer.Import(ctx, &benchmarkimport.Options{
			OrganizationSlug: opts.org,
			OperatorHandle:   opts.operator,
			Dir:              results,
			SuiteRunID:       suite,
			Caps: benchmarkimport.Caps{
				FileBytes:    opts.fileCap,
				AttemptBytes: opts.attemptCap,
			},
		})
		if err != nil {
			// Named with its suite, because an import over every suite in
			// the store fails on one of them and the operator's next
			// question is which.
			return fmt.Errorf("import suite %s: %w", suite, err)
		}
		printImport(suite, result)
	}
	return nil
}

// printImport reports what one suite's import did.
//
// Every count that could be zero for two different reasons is printed with
// the reason beside it. "0 calls" means one thing when the usage log was
// read and another when it could not be, and an operator reading a summary
// is exactly who needs to know which.
func printImport(suite string, result *benchmarkimport.Result) {
	imported, calls, unavailable := 0, 0, 0
	for index := range result.Attempts {
		attempt := &result.Attempts[index]
		if attempt.Imported {
			imported++
		}
		calls += attempt.Calls
		if attempt.Imported && attempt.CallsUnavailable != "" {
			unavailable++
		}
	}
	fmt.Printf("suite %s: %d attempts (%d newly imported, %d already present), %d llm calls\n",
		suite, len(result.Attempts), imported, len(result.Attempts)-imported, calls)
	if unavailable > 0 {
		fmt.Printf("  %d imported attempts recorded no calls: their usage log could not be read\n", unavailable)
	}
	if !result.Terminal {
		fmt.Printf("  the suite is still running: no report, and no evidence stored yet\n")
		return
	}
	if result.Report == nil {
		return
	}
	if result.Report.Created {
		fmt.Printf("  report %s written as a DRAFT, holding %d evidence files\n",
			result.Report.ArtifactID, result.Report.Attachments)
		fmt.Printf("  %s\n", benchmarkimport.DraftBanner)
	} else {
		fmt.Printf("  report %s already written; it still accounts for every attempt\n",
			result.Report.ArtifactID)
	}
	if result.Report.SkippedEvidence > 0 {
		// Loudly, and on stderr. A cap that drops work quietly reads as
		// "there was nothing more to import".
		fmt.Fprintf(os.Stderr, "  %d evidence files were NOT stored (caps or links); "+
			"they are named in the report payload\n", result.Report.SkippedEvidence)
	}
}

// runBenchmarkShow reads one suite back out of the plane.
//
// This is the "queried back" half of the item's exit criterion, and it
// deliberately reads the PLANE rather than the results store: a view
// assembled from the files on disk would prove the files exist, which
// nobody doubted.
func runBenchmarkShow(ctx context.Context, cfg *stack.Config, opts *runOptions) error {
	switch {
	case opts.org == "":
		return errors.New("benchmark show needs -org <slug>")
	case len(opts.suites) != 1:
		// Exactly one, unlike import. Import discovers suites from the
		// store's own layout; the plane has no equivalent listing, and
		// inventing one here would be a query with no seam behind it.
		return errors.New("benchmark show needs exactly one -suite <id>")
	}

	seam, err := openSeam(ctx, cfg)
	if err != nil {
		return err
	}
	defer seam.Close()

	view, err := benchmarkimport.Describe(ctx, seam, opts.org, opts.suites[0])
	if err != nil {
		return fmt.Errorf("read suite %s back: %w", opts.suites[0], err)
	}
	printView(view)
	return nil
}

// printView renders one suite.
func printView(view *benchmarkimport.View) {
	fmt.Printf("suite %s\n  first imported %s\n  %d attempts\n",
		view.SuiteRunID, view.FirstImportedAt.Format("2006-01-02 15:04:05"), len(view.Attempts))
	for index := range view.Attempts {
		attempt := &view.Attempts[index]
		verdict := attempt.Verdict
		if attempt.FailureKind != "" {
			verdict += " (" + attempt.FailureKind + ")"
		}
		fmt.Printf("    %-12s %s / %s\n      %s\n",
			verdict, attempt.StoryID, attempt.ConfigName, attempt.RunID)
	}

	if view.Report == nil {
		fmt.Printf("  no report: the suite had not stopped when it was imported\n")
		return
	}
	printReport(view.Report)
}

// printReport renders the report and what it holds.
func printReport(report *benchmarkimport.ViewReport) {
	fmt.Printf("  report %s [%s]\n    %s\n", report.ArtifactID, report.Status, report.Summary)
	if report.Draft() {
		// The banner is the package's constant rather than a string spelled
		// out here, so the words a reader is warned with cannot drift from
		// the rule that says they must be warned.
		fmt.Printf("    %s\n", benchmarkimport.DraftBanner)
		fmt.Printf("    nobody has reviewed this; acceptance is a separate act by a principal\n")
		fmt.Printf("    that is not its author\n")
	}
	fmt.Printf("    holds %d pinned references:\n", len(report.Pins))
	for index := range report.Pins {
		pin := &report.Pins[index]
		if pin.Path != "" {
			fmt.Printf("      %-11s %s (%s, %d bytes)\n", pin.Kind, pin.Path, pin.Description, pin.SizeBytes)
			continue
		}
		fmt.Printf("      %-11s %s %s\n", pin.Kind, pin.Description, pin.Target)
	}
	if len(report.SkippedEvidence) == 0 {
		return
	}
	fmt.Printf("    %d evidence files are NOT held by this report:\n", len(report.SkippedEvidence))
	for index := range report.SkippedEvidence {
		skip := &report.SkippedEvidence[index]
		fmt.Printf("      %s: %s (%s)\n", skip.Path, skip.Reason, skip.Detail)
	}
}
