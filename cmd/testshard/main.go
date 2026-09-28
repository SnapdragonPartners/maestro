// Command testshard prints the CI shard assignment of a package's
// integration tests, read from the `//ci:shard N` directives beside them.
//
// It is what the workflow calls so that the `-run` pattern each shard job
// passes to `go test` is computed from the tree at that commit, never
// written into the workflow by hand. See package testshard for the rules.
//
// With -shard, it prints the shard's selector as KEY=VALUE lines in the
// format $GITHUB_ENV takes, for a job to append there. The pattern holds
// shell metacharacters, so a shell reading the lines must split on the
// first `=` rather than evaluate them:
//
//	SHARD_RUN=^(TestBackupRestoreRoundTrip|TestResetBlocksForTheWholeOfARestore)$
//	SHARD_TESTS=2
//
// Without -shard, it prints every test with its shard, one per line, and
// exits non-zero if the assignment breaks a rule -- the same verdict the
// package's own guard test reaches, available without running the suite.
package main

import (
	"flag"
	"fmt"
	"os"

	"orchestrator/internal/testshard"
)

func main() {
	dir := flag.String("dir", "", "the package directory whose tests are sharded")
	tag := flag.String("tag", "integration", "the build tag under which the sharded tests exist")
	shard := flag.Int("shard", -1, "print the selector for this shard id; omit to list the whole assignment")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: testshard -dir DIR [-tag TAG] [-shard N]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *dir == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	assignment, err := testshard.Discover(*dir, *tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *shard < 0 {
		for id, names := range assignment.Shards {
			for _, name := range names {
				fmt.Printf("%d\t%s\n", id, name)
			}
		}
		return
	}
	if *shard >= len(assignment.Shards) {
		fmt.Fprintf(os.Stderr, "shard %d does not exist: the assignment has shards 0..%d\n", *shard, len(assignment.Shards)-1)
		os.Exit(1)
	}
	fmt.Printf("SHARD_RUN=%s\nSHARD_TESTS=%d\n", assignment.RunPattern(*shard), assignment.Count(*shard))
}
