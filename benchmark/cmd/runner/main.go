// The runner CLI executes golden stories against benchmark targets and
// records normalized results (ADR 0025, design_engine.md).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/engine"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/safe"
	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/results"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
	"github.com/SnapdragonPartners/maestro/benchmark/target/faketarget"
	"github.com/SnapdragonPartners/maestro/benchmark/target/v1target"
)

const usage = `usage: runner <command> [flags]

commands:
  validate   load stories and configs, print what would run (no execution)
  run        execute the suite matrix and record results
  list       enumerate suite runs in a results store

run 'runner <command> -h' for that command's flags.`

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the subcommand and returns the process exit code; keeping
// os.Exit out of this function lets defers (signal handling) run.
func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch args[0] {
	case "validate":
		err = cmdValidate(args[1:])
	case "run":
		err = cmdRun(ctx, args[1:])
	case "list":
		err = cmdList(args[1:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		return 1
	}
	return 0
}

// adapters is the registry of executable targets. Item 8 adds the
// single-agent baseline. The fake is wired for end-to-end smoke of the
// runner itself (no target, no tokens).
func adapters() map[string]target.Adapter {
	return map[string]target.Adapter{
		"fake":          faketarget.New(),
		"v1-as-patched": v1target.New(),
	}
}

// closeAdapters tears down adapter-owned infrastructure (the shared Gitea
// container and volume) after the suite. Failures are returned, not merely
// printed: a runner that leaks infrastructure must not exit successfully.
func closeAdapters(registry map[string]target.Adapter) error {
	var errs []error
	for name, adapter := range registry {
		if closer, ok := safe.As[io.Closer](adapter); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close adapter %s: %w", name, err))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("adapter teardown: %w", err)
	}
	return nil
}

func loadInputs(storiesPath, configsPath string) ([]*story.Loaded, []*mph.Loaded, error) {
	stories, err := loadStories(storiesPath)
	if err != nil {
		return nil, nil, err
	}
	bundles, err := loadBundles(configsPath)
	if err != nil {
		return nil, nil, err
	}
	return stories, bundles, nil
}

func loadStories(path string) ([]*story.Loaded, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stories path: %w", err)
	}
	if info.IsDir() {
		stories, loadErr := story.LoadDir(path)
		if loadErr != nil {
			return nil, fmt.Errorf("load stories: %w", loadErr)
		}
		return stories, nil
	}
	one, err := story.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load story: %w", err)
	}
	return []*story.Loaded{one}, nil
}

func loadBundles(path string) ([]*mph.Loaded, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("configs path: %w", err)
	}
	if info.IsDir() {
		bundles, loadErr := mph.LoadDir(path)
		if loadErr != nil {
			return nil, fmt.Errorf("load configs: %w", loadErr)
		}
		return bundles, nil
	}
	one, err := mph.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return []*mph.Loaded{one}, nil
}

func filterStories(stories []*story.Loaded, id string) []*story.Loaded {
	if id == "" {
		return stories
	}
	var out []*story.Loaded
	for _, s := range stories {
		if s.Definition.ID == id {
			out = append(out, s)
		}
	}
	return out
}

func filterBundles(bundles []*mph.Loaded, name string) []*mph.Loaded {
	if name == "" {
		return bundles
	}
	var out []*mph.Loaded
	for _, b := range bundles {
		if b.Bundle.Name == name {
			out = append(out, b)
		}
	}
	return out
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	storiesPath := fs.String("stories", "stories", "story definition file or directory")
	configsPath := fs.String("configs", "configs", "MPH bundle file or directory")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	stories, bundles, err := loadInputs(*storiesPath, *configsPath)
	if err != nil {
		return err
	}
	for _, s := range stories {
		fmt.Printf("story  %-28s %s  fixture %s @ %.12s\n", s.Definition.ID, s.Hash[:19], s.Definition.Fixture.Repo, s.Definition.Fixture.Commit)
	}
	for _, b := range bundles {
		fmt.Printf("config %-28s %s  adapter %s\n", b.Bundle.Name, b.Hash[:19], b.Bundle.Harness.Adapter)
	}
	fmt.Printf("%d stories × %d configs validated\n", len(stories), len(bundles))
	return nil
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	storiesPath := fs.String("stories", "stories", "story definition file or directory")
	configsPath := fs.String("configs", "configs", "MPH bundle file or directory")
	storyID := fs.String("story", "", "run only this story ID")
	configName := fs.String("config", "", "run only this config name")
	repeats := fs.Int("repeats", 1, "attempts per story per config (N)")
	resultsDir := fs.String("results", "results", "results store directory")
	workdir := fs.String("workdir", "", "workspace root (default: temp dir)")
	suiteID := fs.String("suite-id", "", "suite run ID (default: generated)")
	keepInfra := fs.Bool("keep-infra", false, "keep adapter infrastructure (Gitea) running after the suite")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	stories, bundles, err := loadInputs(*storiesPath, *configsPath)
	if err != nil {
		return err
	}
	stories = filterStories(stories, *storyID)
	bundles = filterBundles(bundles, *configName)
	store, err := results.Open(*resultsDir)
	if err != nil {
		return fmt.Errorf("open results store: %w", err)
	}
	wd := *workdir
	if wd == "" {
		wd, err = os.MkdirTemp("", "golden-runner-")
		if err != nil {
			return fmt.Errorf("workdir: %w", err)
		}
	}
	id := *suiteID
	if id == "" {
		id, err = generateSuiteID()
		if err != nil {
			return err
		}
	}
	registry := adapters()
	eng := &engine.Engine{
		Adapters: registry,
		Store:    store,
		Workdir:  wd,
		Logf: func(format string, a ...any) {
			fmt.Printf(format+"\n", a...)
		},
	}
	manifest, err := eng.RunSuite(ctx, engine.SuiteParams{
		SuiteRunID: id,
		Stories:    stories,
		Bundles:    bundles,
		Repeats:    *repeats,
	})
	if manifest != nil {
		fmt.Printf("suite %s: %s (%d attempts; charged $%.2f, observed $%.2f of $%.2f cap)\n",
			manifest.SuiteRunID, manifest.StopReason, len(manifest.Attempts), manifest.ChargedUSD, manifest.ObservedUSD, manifest.CapUSD)
	}
	var closeErr error
	if !*keepInfra {
		closeErr = closeAdapters(registry)
	}
	if err != nil {
		err = fmt.Errorf("run suite: %w", err)
	}
	if joined := errors.Join(err, closeErr); joined != nil {
		return fmt.Errorf("%w", joined)
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	resultsDir := fs.String("results", "results", "results store directory")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	store, err := results.Open(*resultsDir)
	if err != nil {
		return fmt.Errorf("open results store: %w", err)
	}
	ids, err := store.SuiteRunIDs()
	if err != nil {
		return fmt.Errorf("list suites: %w", err)
	}
	for _, id := range ids {
		line := id
		if manifest, err := store.ReadManifest(id); err == nil {
			line = fmt.Sprintf("%s  %s (%d attempts)", id, manifest.StopReason, len(manifest.Attempts))
		}
		fmt.Println(line)
	}
	if len(ids) == 0 {
		fmt.Println("no suite runs found")
	}
	return nil
}

// generateSuiteID builds a lowercase, filename-safe unique suite ID with a
// UTC timestamp prefix for human sorting.
func generateSuiteID() (string, error) {
	raw := make([]byte, 3)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("suite id entropy: %w", err)
	}
	return fmt.Sprintf("suite-%s-%s", time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(raw)), nil
}
