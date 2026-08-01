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
	force := flag.Bool("force", false, "for reset, restore and force-version: proceed without the interactive confirmation")
	forceVersion := flag.Int("version", -1, "for force-version: the schema version to record")
	destination := flag.String("to", "", "for backup: the archive directory to create (must not exist)")
	source := flag.String("from", "", "for restore: the archive directory to restore from")
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
	err := run(ctx, flag.Arg(0), runOptions{
		composeFile:  *composeFile,
		force:        *force,
		forceVersion: *forceVersion,
		destination:  *destination,
		source:       *source,
	})
	stopSignals()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dataplanectl: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: dataplanectl [flags] <up|down|reset|migrate|force-version|backup|restore|verify>

  up       start Postgres and MinIO, wait until usable, apply migrations (idempotent)
  down     stop the containers, leaving all data in place
  reset    stop the containers and DELETE the contents of the data directories
  force-version
           repair a DIRTY schema version by recording a version without
           running migrations, for recovering from a failed migration.
           Requires -version, which -- like every flag here -- must come
           BEFORE the command, since Go's flag parsing stops at the first
           positional argument:
               dataplanectl -version 10 force-version
  migrate  apply pending migrations to an already-running stack
  backup   stop the plane, copy the data root to -to, restart what was running.
           The archive excludes the root-of-trust key by design, so restoring
           it elsewhere needs the key file too (or new-key recovery).
  restore  replace the data root from the archive at -from. Requires -force
           when the data root already holds a plane.
  verify   recompute every stored digest and read every attachment, which is
           what proves a restored cluster and object store still agree

flags:
`)
	flag.PrintDefaults()
}

// runOptions carries the flags a command may need. A struct rather than a
// growing parameter list: the verbs share a launcher but not their inputs.
type runOptions struct {
	composeFile  string
	destination  string
	source       string
	forceVersion int
	force        bool
}

func run(ctx context.Context, command string, opts runOptions) error {
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
		return runUp(ctx, cfg, opts)

	case "migrate":
		return runMigrate(ctx, cfg)

	case "down":
		return runDown(ctx, cfg, opts)

	case "force-version":
		return runForceVersion(cfg, opts.force, opts.forceVersion)

	case "reset":
		if !opts.force {
			if !confirmReset(cfg) {
				fmt.Println("reset cancelled")
				return nil
			}
		}
		if err := stack.Reset(ctx, cfg, opts.composeFile); err != nil {
			return fmt.Errorf("reset the data plane: %w", err)
		}
		return nil

	case "backup":
		return runBackup(ctx, cfg, opts)

	case "restore":
		return runRestore(ctx, cfg, opts)

	case "verify":
		return runVerify(ctx, cfg, opts)

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

// runBackup copies the data root to a new archive directory.
func runBackup(ctx context.Context, cfg *stack.Config, opts runOptions) error {
	if opts.destination == "" {
		return errors.New("backup needs -to <directory>, which must not already exist")
	}
	if err := stack.Backup(ctx, cfg, opts.composeFile, opts.destination); err != nil {
		return fmt.Errorf("back up the data plane: %w", err)
	}
	fmt.Printf("archive written to %s\n"+
		"  the root-of-trust key is NOT in it: restoring elsewhere needs %s as well\n",
		opts.destination, cfg.Roots.KeyPath())
	return nil
}

// runRestore replaces the data root from an archive.
//
// Guarded like reset, and for a stronger reason: reset destroys data the
// operator asked to destroy, while a restore onto a populated root destroys
// data they may not have realised was there.
func runRestore(ctx context.Context, cfg *stack.Config, opts runOptions) error {
	if opts.source == "" {
		return errors.New("restore needs -from <directory>")
	}
	if err := stack.Restore(ctx, cfg, opts.composeFile, opts.source, opts.force); err != nil {
		return fmt.Errorf("restore the data plane: %w", err)
	}
	fmt.Printf("restored from %s\n", opts.source)
	return nil
}

// runVerify reports what verification checked as well as what it found.
//
// Both halves matter: an empty problem list means nothing without the counts
// beside it, because a pass that walked nothing reports exactly the same
// thing as a healthy plane.
func runVerify(ctx context.Context, cfg *stack.Config, _ runOptions) error {
	report, err := stack.Verify(ctx, cfg)
	if err != nil {
		return fmt.Errorf("verify the data plane: %w", err)
	}

	fmt.Printf("checked %d organizations: %d management artifacts, %d audit artifacts, %d attachments",
		report.Organizations, report.ManagementArtifacts, report.AuditArtifacts, report.Attachments)
	if report.Skipped > 0 {
		fmt.Printf(" (%d attachments removed while the pass ran)", report.Skipped)
	}
	fmt.Println()

	if report.Healthy() {
		fmt.Println("no problems found")
		return nil
	}
	for i := range report.Problems {
		problem := &report.Problems[i]
		fmt.Printf("  %s %s: %s\n", problem.Kind, problem.ID, problem.Detail)
	}
	return fmt.Errorf("verification found %d problem(s)", len(report.Problems))
}

func runUp(ctx context.Context, cfg *stack.Config, opts runOptions) error {
	if err := stack.Up(ctx, cfg, opts.composeFile); err != nil {
		return fmt.Errorf("bring the data plane up: %w", err)
	}
	fmt.Printf("data plane ready\n  postgres  127.0.0.1:%d/%s\n  objects   http://127.0.0.1:%d\n  console   http://127.0.0.1:%d\n",
		cfg.PGPort, cfg.Database, cfg.MinIOPort, cfg.MinIOConsolePort)
	return nil
}

func runMigrate(ctx context.Context, cfg *stack.Config) error {
	if err := stack.Migrate(ctx, cfg); err != nil {
		return fmt.Errorf("migrate the data plane: %w", err)
	}
	fmt.Println("schema up to date")
	return nil
}

func runDown(ctx context.Context, cfg *stack.Config, opts runOptions) error {
	if err := stack.Down(ctx, cfg, opts.composeFile); err != nil {
		return fmt.Errorf("bring the data plane down: %w", err)
	}
	return nil
}
