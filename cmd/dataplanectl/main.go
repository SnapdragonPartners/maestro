// Command dataplanectl brings Maestro's local data plane up and down.
//
// It is the supported entry point to deploy/dataplane/compose.yaml: it
// resolves the storage roots, pre-creates and verifies the bind-mount
// sources, derives credentials from the root-of-trust key, writes the
// bootstrap pointer, and waits for both services to be usable. Invoked by
// `make dataplane-up` / `-down` / `-reset`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/stack"
)

func main() {
	composeFile := flag.String("compose", stack.DefaultComposeFile, "path to the data-plane compose file")
	force := flag.Bool("force", false, "for reset and force-version: proceed without the interactive confirmation")
	forceVersion := flag.Int("version", -1, "for force-version: the schema version to record")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}

	// Ctrl-C during a cold initdb should stop waiting, not orphan the
	// operation: Compose has already been told to start, so the containers
	// keep coming up and a later `up` picks them up.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, flag.Arg(0), *composeFile, *force, *forceVersion)
	stopSignals()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dataplanectl: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: dataplanectl [flags] <up|down|reset|migrate|force-version>

  up       start Postgres and MinIO, wait until usable, apply migrations (idempotent)
  down     stop the containers, leaving all data in place
  reset    stop the containers and DELETE the contents of the data directories
  force-version -version <n>
           repair a DIRTY schema version by recording <n> without running
           migrations; for recovering from a failed migration
  migrate  apply pending migrations to an already-running stack

flags:
`)
	flag.PrintDefaults()
}

func run(ctx context.Context, command, composeFile string, force bool, forceVersion int) error {
	roots, err := paths.Resolve()
	if err != nil {
		return fmt.Errorf("resolve storage roots: %w", err)
	}
	cfg, err := stack.NewConfig(roots)
	if err != nil {
		return fmt.Errorf("build data-plane config: %w", err)
	}

	switch command {
	case "up":
		if err := stack.Up(ctx, cfg, composeFile); err != nil {
			return fmt.Errorf("bring the data plane up: %w", err)
		}
		fmt.Printf("data plane ready\n  postgres  127.0.0.1:%d/%s\n  objects   http://127.0.0.1:%d\n  console   http://127.0.0.1:%d\n",
			cfg.PGPort, cfg.Database, cfg.MinIOPort, cfg.MinIOConsolePort)
		return nil

	case "migrate":
		if err := stack.Migrate(ctx, cfg); err != nil {
			return fmt.Errorf("migrate the data plane: %w", err)
		}
		fmt.Println("schema up to date")
		return nil

	case "down":
		if err := stack.Down(ctx, cfg, composeFile); err != nil {
			return fmt.Errorf("bring the data plane down: %w", err)
		}
		return nil

	case "force-version":
		return runForceVersion(cfg, force, forceVersion)

	case "reset":
		if !force {
			if !confirmReset(cfg) {
				fmt.Println("reset cancelled")
				return nil
			}
		}
		if err := stack.Reset(ctx, cfg, composeFile); err != nil {
			return fmt.Errorf("reset the data plane: %w", err)
		}
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

// runForceVersion repairs a dirty schema version.
//
// Guarded like reset, because both can quietly destroy the ability to
// reason about the schema: reset by removing the data, this by asserting a
// version that may not be true.
func runForceVersion(cfg *stack.Config, force bool, version int) error {
	if version < 0 {
		return errors.New("force-version requires -version <n>")
	}
	if !force && !confirmForceVersion(version) {
		fmt.Println("force-version cancelled")
		return nil
	}
	if err := stack.ForceVersion(cfg, version); err != nil {
		return fmt.Errorf("force the schema version: %w", err)
	}
	fmt.Printf("schema version forced to %d\n", version)
	return nil
}

// confirmForceVersion requires an explicit yes before recording a schema
// version that was not reached by running migrations.
//
// Guarded like reset, because both can quietly destroy the ability to
// reason about the schema: reset by removing the data, this by asserting a
// version that may not be true. A wrong force is worse in one respect --
// it leaves no trace and nothing later can detect it.
func confirmForceVersion(version int) bool {
	fmt.Printf("Record schema version %d WITHOUT running migrations?\n"+
		"This changes metadata only. If the schema is not really at %d, nothing will detect the "+
		"disagreement.\nType 'yes' to continue: ", version, version)

	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		// Unreadable stdin is a "no", for the same reason as reset.
		return false
	}
	return answer == "yes"
}

// confirmReset requires an explicit yes before destroying the data root's
// contents. ADR 0022 spent an amendment making that data durable, so it
// does not get deleted on a typo.
func confirmReset(cfg *stack.Config) bool {
	fmt.Printf("This deletes ALL data under %s (Postgres cluster and object store).\nType 'yes' to continue: ", cfg.Roots.Data)

	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		// An unreadable, closed or empty stdin is a "no", never an error:
		// reset must not proceed because a prompt could not be read, and it
		// must not fail loudly either — declining is the safe outcome.
		// Scripted callers pass -force.
		return false
	}
	return answer == "yes"
}
