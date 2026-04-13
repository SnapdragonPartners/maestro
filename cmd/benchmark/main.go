// Command benchmark runs SWE-EVO benchmark instances against Maestro.
//
// Usage:
//
//	benchmark -dataset instances.json -repos-dir ./bare-repos -container swe-eval:python
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"orchestrator/pkg/benchmark"
	"orchestrator/pkg/logx"
)

type cliFlags struct {
	dataset    string
	reposDir   string
	output     string
	results    string
	archiveDir string
	baseDir    string
	maestroBin string
	container  string
	instances  string
	timeout    time.Duration
}

func parseFlags() *cliFlags {
	f := &cliFlags{}
	flag.StringVar(&f.dataset, "dataset", "", "Path to SWE-EVO instances JSON (required)")
	flag.StringVar(&f.reposDir, "repos-dir", "", "Path to bare clones of upstream repos (required)")
	flag.StringVar(&f.output, "output", "preds.json", "Predictions output file path")
	flag.StringVar(&f.results, "results", "results.json", "Full results output file path")
	flag.StringVar(&f.archiveDir, "archive-dir", "archives", "Artifact archive directory")
	flag.StringVar(&f.baseDir, "base-dir", "runs", "Per-instance project directory root")
	flag.StringVar(&f.maestroBin, "maestro-bin", "maestro", "Path to maestro binary")
	flag.DurationVar(&f.timeout, "timeout", 60*time.Minute, "Per-instance timeout")
	flag.StringVar(&f.instances, "instances", "", "Comma-separated instance IDs to run (empty = all)")
	flag.StringVar(&f.container, "container", "", "Default container image (required if instances lack eval_image)")
	flag.Parse()
	return f
}

func main() {
	os.Exit(run())
}

func run() int {
	flags := parseFlags()
	logger := logx.NewLogger("benchmark")

	runInstances := loadAndValidate(flags, logger)
	if runInstances == nil {
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Warn("Received signal %s, cancelling...", sig)
		cancel()
	}()

	giteaMgr, setupErr := setupInfrastructure(ctx, flags, logger)
	if setupErr != nil {
		logger.Error("Infrastructure setup: %v", setupErr)
		return 1
	}

	opts := &benchmark.RunOptions{
		BaseDir:        flags.baseDir,
		MaestroBin:     flags.maestroBin,
		ContainerImage: flags.container,
		Timeout:        flags.timeout,
		ArchiveDir:     flags.archiveDir,
	}

	allResults := make([]benchmark.Result, 0, len(runInstances))
	for i := range runInstances {
		if ctx.Err() != nil {
			logger.Warn("Context cancelled, stopping at instance %d/%d", i, len(runInstances))
			break
		}

		logger.Info("=== Instance %d/%d: %s ===", i+1, len(runInstances), runInstances[i].InstanceID)
		result := benchmark.RunInstance(ctx, &runInstances[i], giteaMgr, opts)
		allResults = append(allResults, result)

		logger.Info("Result: %s (%.1fs, %d bytes patch)",
			result.Outcome, result.ElapsedSecs, len(result.Patch))
	}

	writeOutputs(allResults, flags, logger)
	printSummary(allResults, logger)
	return 0
}

func setupInfrastructure(ctx context.Context, flags *cliFlags, logger *logx.Logger) (*benchmark.BenchGitea, error) {
	for _, dir := range []string{flags.archiveDir, flags.baseDir} {
		if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, mkErr)
		}
	}

	giteaMgr := benchmark.NewBenchGitea(flags.reposDir)
	logger.Info("Starting Gitea...")
	if giteaErr := giteaMgr.EnsureRunning(ctx); giteaErr != nil {
		return nil, fmt.Errorf("start Gitea: %w", giteaErr)
	}
	logger.Info("Gitea ready")
	return giteaMgr, nil
}

// loadAndValidate loads instances from the dataset, applies filters, validates images.
// Returns nil on validation failure (caller should exit).
func loadAndValidate(flags *cliFlags, logger *logx.Logger) []benchmark.Instance {
	if flags.dataset == "" {
		logger.Error("Missing required flag: -dataset")
		flag.Usage()
		return nil
	}
	if flags.reposDir == "" {
		logger.Error("Missing required flag: -repos-dir")
		flag.Usage()
		return nil
	}

	allInstances, err := benchmark.LoadInstances(flags.dataset)
	if err != nil {
		logger.Error("Load instances: %v", err)
		return nil
	}
	logger.Info("Loaded %d instances from %s", len(allInstances), flags.dataset)

	var filterIDs []string
	if flags.instances != "" {
		filterIDs = strings.Split(flags.instances, ",")
		for i := range filterIDs {
			filterIDs[i] = strings.TrimSpace(filterIDs[i])
		}
	}
	runInstances := benchmark.FilterInstances(allInstances, filterIDs)
	if len(runInstances) == 0 {
		logger.Error("No instances to run (filter matched nothing)")
		return nil
	}
	logger.Info("Running %d instances", len(runInstances))

	for i := range runInstances {
		if runInstances[i].EvalImage == "" && flags.container == "" {
			logger.Error("Instance %s has no eval_image and no -container flag provided", runInstances[i].InstanceID)
			return nil
		}
	}

	return runInstances
}

func writeOutputs(results []benchmark.Result, flags *cliFlags, logger *logx.Logger) {
	if predsErr := benchmark.WritePreds(results, flags.output); predsErr != nil {
		logger.Error("Write preds: %v", predsErr)
	} else {
		logger.Info("Predictions written to %s", flags.output)
	}

	if resultsErr := benchmark.WriteFullResults(results, flags.results); resultsErr != nil {
		logger.Error("Write results: %v", resultsErr)
	} else {
		logger.Info("Full results written to %s", flags.results)
	}
}

func printSummary(results []benchmark.Result, logger *logx.Logger) {
	counts := make(map[benchmark.Outcome]int)
	var totalElapsed float64
	var patchCount int

	for i := range results {
		counts[results[i].Outcome]++
		totalElapsed += results[i].ElapsedSecs
		if results[i].Patch != "" {
			patchCount++
		}
	}

	logger.Info("=== Summary ===")
	logger.Info("Total: %d instances", len(results))
	logger.Info("Success: %d", counts[benchmark.OutcomeSuccess])
	logger.Info("Terminal failure: %d", counts[benchmark.OutcomeTerminalFailure])
	logger.Info("Stalled: %d", counts[benchmark.OutcomeStalled])
	logger.Info("Timeout: %d", counts[benchmark.OutcomeTimeout])
	logger.Info("Process error: %d", counts[benchmark.OutcomeProcessError])
	logger.Info("Patches collected: %d/%d", patchCount, len(results))

	if len(results) > 0 {
		logger.Info("Total time: %s", formatDuration(totalElapsed))
		logger.Info("Avg time: %s", formatDuration(totalElapsed/float64(len(results))))
	}
}

func formatDuration(secs float64) string {
	d := time.Duration(secs * float64(time.Second))
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", secs)
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}
