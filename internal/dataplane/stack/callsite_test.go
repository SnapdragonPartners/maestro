package stack

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"orchestrator/internal/testshard"
)

// guardedVerbs maps each lifecycle entry point to the lifecycle constant it
// must guard itself with.
//
// The exported functions, not the internals: the lock-assuming forms are
// called from inside operations that have already made the decision, and
// requiring a guard there would forbid restore from verifying under its own
// marker.
//
//nolint:gochecknoglobals // Immutable expectation table for the test below.
var guardedVerbs = map[string]string{
	"Up":           "lifecycleUp",
	"Migrate":      "lifecycleMigrate",
	"ForceVersion": "lifecycleForceVersion",
	"Backup":       "lifecycleBackup",
	"Restore":      "lifecycleRestore",
	"Verify":       "lifecycleVerify",
	"RecoverKey":   "lifecycleRecoverKey",
	"OpenSeam":     "lifecycleUse",
}

// unguardedVerbs are the entry points deliberately NOT guarded, each with
// the reason it is safe against both marked states.
//
// Being listed here is a decision, not an omission. That distinction is the
// whole point: an entry point missing from BOTH tables fails the test, so a
// verb added later — new-key recovery is the next one — cannot become
// unguarded by nobody noticing it.
//
//nolint:gochecknoglobals // Immutable expectation table for the test below.
var unguardedVerbs = map[string]string{
	"Down":  "stopping an already-stopped torn or unverified plane cannot make it worse",
	"Reset": "discarding the plane is a way out of both states, and sweeps both markers",
}

// The marker policy is worthless if nothing consults it, and testing the
// policy helper cannot tell the difference.
//
// This is the same defect, one layer up, that mutation testing found in the
// restore tests: asserting a helper's behaviour proves nothing about whether
// its callers use it. Deleting the guardRestoreState call from Migrate
// leaves every table-driven test in marker_test.go green, because none of
// them calls Migrate. So the call sites are checked structurally, by parsing
// this package.
//
// Bidirectional on purpose. One direction catches a verb that stops
// guarding; the other catches a verb removed from the table but still in the
// package, which would silently shrink what is enforced.
func TestEveryLifecycleVerbGuardsOnTheMarker(t *testing.T) {
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	// Entry points are DISCOVERED from the package rather than read off the
	// table. A table-driven parser only inspects what the table already
	// names, so deleting a verb from it would make the test ignore that verb
	// and pass — the failure this whole test exists to prevent, one level up.
	found := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil {
				continue
			}
			if !isLifecycleEntryPoint(fn) {
				continue
			}
			argument, _ := guardArgument(fn)
			found[fn.Name.Name] = argument
		}
	}

	// Every DISCOVERED entry point must have been classified.
	for verb, got := range found {
		want, guarded := guardedVerbs[verb]
		reason, exempt := unguardedVerbs[verb]
		switch {
		case !guarded && !exempt:
			t.Errorf("%s is a lifecycle entry point in neither guardedVerbs nor unguardedVerbs: "+
				"decide whether it may run against a torn restore", verb)
		case exempt && got != "":
			t.Errorf("%s is listed as unguarded (%s) but calls guardRestoreState: the tables disagree with the code",
				verb, reason)
		case guarded && got == "":
			t.Errorf("%s does not call guardRestoreState: it would act on a torn restore, or on a "+
				"plane nothing has ever verified", verb)
		case guarded && got != want:
			t.Errorf("%s guards on %s, want %s: guarding under the wrong operation reads the wrong policy row",
				verb, got, want)
		}
	}

	// And every classified verb must still exist, so a table entry cannot
	// outlive the function it describes and quietly enforce nothing.
	for verb := range guardedVerbs {
		if _, present := found[verb]; !present {
			t.Errorf("guardedVerbs names %s, which is not a lifecycle entry point in this package", verb)
		}
	}
	for verb := range unguardedVerbs {
		if _, present := found[verb]; !present {
			t.Errorf("unguardedVerbs names %s, which is not a lifecycle entry point in this package", verb)
		}
	}
}

// isLifecycleEntryPoint reports whether a function is an exported operation
// against a plane.
//
// The structural definition is "exported, and takes a *Config" — which is
// what every lifecycle verb has in common and what a new one will have too.
// Deriving it beats listing it: a list is exactly the thing that goes stale.
func isLifecycleEntryPoint(fn *ast.FuncDecl) bool {
	if !fn.Name.IsExported() || fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		pointer, isPointer := field.Type.(*ast.StarExpr)
		if !isPointer {
			continue
		}
		if named, isNamed := pointer.X.(*ast.Ident); isNamed && named.Name == "Config" {
			return true
		}
	}
	return false
}

// guardArgument reports the lifecycle constant a function passes to
// guardRestoreState, if it calls it at all.
//
// guardRestoreState and not guardRestoreMarker: the combined guard is what
// applies BOTH marker policies, and a verb that reached past it to the torn
// half alone would be guarded against the failure it remembered and open to
// the other.
func guardArgument(fn *ast.FuncDecl) (string, bool) {
	var argument string
	ast.Inspect(fn, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdent := call.Fun.(*ast.Ident)
		if !isIdent || name.Name != "guardRestoreState" || len(call.Args) != 2 {
			return true
		}
		if operation, ok := call.Args[1].(*ast.Ident); ok {
			argument = operation.Name
		}
		return false
	})
	return argument, argument != ""
}

// The same property one level down: `up`'s own debt-bearing shutdown must be
// armed before Compose starts anything.
//
// This is the defect the test below covers, rediscovered inside the function
// that test's subject calls. `up` invokes Compose and then does four more
// things — readiness, bucket setup, migrations, claim reconciliation — any
// of which can fail with the containers already running. When the only
// shutdown lived beside the settlement step, every one of those returned a
// live plane carrying unsettled verification debt, which is exactly what
// D4a forbids.
//
// So the arming point is asserted to precede the FIRST thing that starts
// anything, and the disarm to follow the settlement. Checked structurally
// for the reason the next test is: this is a property of statement ORDER,
// which a parser sees exactly and which review demonstrably does not.
func TestUpArmsItsDebtShutdownBeforeStartingAnything(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "stack.go", nil, 0)
	if err != nil {
		t.Fatalf("parse stack.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Name.Name == "up" && fn.Recv == nil {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("up is gone: this guard is enforcing nothing")
	}

	deferAt, armedAt, composeAt, settleAt, disarmedAt := -1, -1, -1, -1, -1
	for i, statement := range body.List {
		if deferred, isDefer := statement.(*ast.DeferStmt); isDefer && deferAt < 0 && callsFunc(deferred, "down") {
			deferAt = i
			// The arming assignment is the declaration of the flag that
			// defer consults, which must exist before it.
			continue
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				if assignsIdent(typed, "settled") {
					if armedAt < 0 {
						armedAt = i
					}
					if assignsTrue(typed, "settled") && disarmedAt < 0 {
						disarmedAt = i
					}
				}
			case *ast.CallExpr:
				name, isIdent := typed.Fun.(*ast.Ident)
				if !isIdent {
					return true
				}
				if name.Name == "compose" && composeAt < 0 {
					composeAt = i
				}
				if name.Name == "settleOutstandingVerification" && settleAt < 0 {
					settleAt = i
				}
			}
			return true
		})
	}

	switch {
	case composeAt < 0:
		t.Fatal("up never calls compose: this guard is enforcing nothing")
	case deferAt < 0:
		t.Fatal("up has no deferred call to down: a failure after Compose starts the containers would " +
			"leave a plane carrying verification debt running for connected writers")
	case armedAt < 0:
		t.Fatal("up never assigns the settled flag: the defer would consult nothing")
	case armedAt > composeAt:
		t.Errorf("the debt flag is set at statement %d but compose runs at %d: readiness, bucket setup, "+
			"migrations and claim reconciliation would each leave an unverified plane running",
			armedAt, composeAt)
	case deferAt > composeAt:
		t.Errorf("the shutdown defer is registered at statement %d but compose runs at %d: a defer "+
			"registered after a call cannot run for a failure inside it", deferAt, composeAt)
	case settleAt < 0:
		t.Fatal("up never calls settleOutstandingVerification: there is no settlement for the debt " +
			"shutdown to be disarmed by, so this guard is enforcing nothing")
	case disarmedAt < 0:
		t.Fatal("up never disarms the debt shutdown: a healthy settlement would stop the plane it just " +
			"vouched for")
	case disarmedAt < settleAt:
		// Compared against SETTLEMENT, not against compose. The rule is
		// "disarm only once the debt is actually paid", and compose is
		// merely something that happens earlier -- a disarm anywhere between
		// compose and settlement would satisfy a compose comparison while
		// leaving the whole exposed region uncovered, which is this
		// package's recurring defect wearing its third hat.
		//
		// Strictly less-than, so a disarm nested INSIDE the settlement
		// statement -- `if err := settle(...); err == nil { settled = true }`
		// -- is accepted. That spelling is correct, and a guard that
		// rejected it would be forbidding a refactor rather than a defect.
		t.Errorf("the debt shutdown is disarmed at statement %d but settlement runs at %d: the plane "+
			"would be vouched for before anything verified it", disarmedAt, settleAt)
	}
}

// assignsIdent reports whether a statement assigns the named variable at
// all, whatever the value. The arming assignment is `settled := !owed`,
// which assignsTrue cannot see.
func assignsIdent(assignment *ast.AssignStmt, target string) bool {
	for _, left := range assignment.Lhs {
		if name, isIdent := left.(*ast.Ident); isIdent && name.Name == target {
			return true
		}
	}
	return false
}

// Shutdown must be armed BEFORE `up` is called, not after it returns.
//
// `up` starts containers and then does four more things — readiness,
// bucket setup, migrations, claim reconciliation — any of which can fail
// with the plane already running. Arming afterwards covers none of them,
// and the difference is invisible in review: both spellings are one
// assignment beside one call.
//
// Checked structurally because the behavioural test needs Docker, and this
// property is about statement ORDER, which a parser can see exactly.
func TestRestoreArmsShutdownBeforeStartingThePlane(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "restore.go", nil, 0)
	if err != nil {
		t.Fatalf("parse restore.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Name.Name == "replaceDataRoot" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("replaceDataRoot is gone: this guard is enforcing nothing")
	}

	// THREE things have to line up, and checking fewer of them leaves the
	// defect reachable:
	//
	//   - the shutdown defer must EXIST — deleting it restores the original
	//     bug outright;
	//   - it must be registered BEFORE `up`, since a defer registered after
	//     a call cannot run for a failure inside that call;
	//   - and the flag it consults must be armed with `true` before `up`
	//     too, or the defer runs and does nothing.
	//
	// An earlier version of this guard checked only the third. It passed
	// while the defer was deleted, and passed while the defer was moved
	// after `up` — so it certified exactly the arrangement it exists to
	// forbid. The lesson is the same one this package keeps relearning:
	// assert the mechanism, not a token near it.
	deferAt, armedAt, upAt := -1, -1, -1
	for i, statement := range body.List {
		if deferred, isDefer := statement.(*ast.DeferStmt); isDefer && deferAt < 0 && callsFunc(deferred, "down") {
			deferAt = i
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				if armedAt < 0 && assignsTrue(typed, "started") {
					armedAt = i
				}
			case *ast.CallExpr:
				if name, isIdent := typed.Fun.(*ast.Ident); isIdent && name.Name == "up" && upAt < 0 {
					upAt = i
				}
			}
			return true
		})
	}

	switch {
	case upAt < 0:
		t.Fatal("replaceDataRoot never calls up: this guard is enforcing nothing")
	case deferAt < 0:
		t.Fatal("replaceDataRoot has no deferred call to down: a failure after the plane starts " +
			"would leave an unverified plane running for connected writers")
	case deferAt > upAt:
		t.Errorf("the shutdown defer is registered at statement %d but up runs at %d: a defer registered "+
			"after a call cannot run for a failure inside it", deferAt, upAt)
	case armedAt < 0:
		t.Fatal("replaceDataRoot never arms shutdown with true: the defer would run and do nothing")
	case armedAt > upAt:
		t.Errorf("shutdown is armed at statement %d but up runs at %d: a failure inside up — readiness, "+
			"migrations, bucket setup, reconciliation — would leave an unverified plane running", armedAt, upAt)
	}
}

// callsFunc reports whether a deferred statement's body calls the named
// function.
func callsFunc(deferred *ast.DeferStmt, target string) bool {
	found := false
	ast.Inspect(deferred, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if name, isIdent := call.Fun.(*ast.Ident); isIdent && name.Name == target {
			found = true
			return false
		}
		return true
	})
	return found
}

// assignsTrue reports whether a statement sets the named variable to the
// literal true.
func assignsTrue(assignment *ast.AssignStmt, target string) bool {
	for i, left := range assignment.Lhs {
		name, isIdent := left.(*ast.Ident)
		if !isIdent || name.Name != target || i >= len(assignment.Rhs) {
			continue
		}
		if value, isIdent := assignment.Rhs[i].(*ast.Ident); isIdent && value.Name == "true" {
			return true
		}
	}
	return false
}

// CI runs this package's integration tests in shards, one runner VM each,
// and the tests a shard runs are chosen by the `//ci:shard N` directive in
// each test's doc comment (issue #374; see internal/testshard for the
// rules). The workflow never names a test: `cmd/testshard` reads the
// directives at each commit and computes the `-run` pattern.
//
// This guard is what makes that safe. A test with no directive would run in
// no shard — and, like the two hand-maintained `-run` alternations this
// replaced, its absence would be silent: it passes locally on macOS and the
// suite stays green. This package has been bitten by enumerations of what
// to cover three times already (the lock table that omitted three verbs,
// the marker table that would have, and the recovery job's pattern, widened
// twice in two rounds). So the assignment is checked against the tests that
// exist, and the workflow's matrix against the assignment: a shard id the
// directives use that the matrix does not list is a shard nobody runs.
func TestEveryIntegrationTestIsInExactlyOneCIShard(t *testing.T) {
	assignment, err := testshard.Discover(".", "integration")
	if err != nil {
		t.Fatal(err)
	}

	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read the CI workflow: %v", err)
	}
	matrix := shardMatrix(t, string(workflow))
	var want []string
	for id := range assignment.Shards {
		want = append(want, strconv.Itoa(id))
	}
	if strings.Join(matrix, ",") != strings.Join(want, ",") {
		t.Errorf("the workflow's shard matrix is [%s] but the directives assign shards [%s]: "+
			"a shard in one and not the other is either a job that runs nothing or tests no job runs",
			strings.Join(matrix, ", "), strings.Join(want, ", "))
	}

	// The job must read the assignment from THIS package, with the tool
	// that this guard just ran: a workflow that computed the pattern some
	// other way, or for some other directory, would be enforced by nothing.
	const invocation = "go run ./cmd/testshard -dir ./internal/dataplane/stack -shard ${{ matrix.shard }}"
	if !strings.Contains(string(workflow), invocation) {
		t.Errorf("the CI workflow does not contain %q: the shard job selects tests some other way "+
			"and this guard is enforcing nothing", invocation)
	}

	// And the runner must CONSUME what the tool produced. The first cut of
	// this guard checked only that the tool was invoked; the review changed
	// the `go test` line to `-run "^TestRecover"` and the guard stayed green
	// with the selector computed and ignored — a false green of exactly the
	// kind the pass floor cannot catch, since twenty recovery tests clear
	// any shard's floor. So the go test command that names $SHARD_RUN is
	// found and read: it must select by that variable, in this package,
	// under the tag, uncached.
	runner := shardRunnerCommand(t, string(workflow))
	for _, want := range []string{"go test ", "-tags=integration", "-count=1", `-run "$SHARD_RUN"`, "./internal/dataplane/stack/"} {
		if !strings.Contains(runner, want) {
			t.Errorf("the shard job's go test command lacks %q: %q", want, runner)
		}
	}
}

// shardRunnerCommand returns the workflow's `go test` command that uses
// $SHARD_RUN, with its backslash continuations joined into one line.
func shardRunnerCommand(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	for i, line := range lines {
		if !strings.Contains(line, `-run "$SHARD_RUN"`) {
			continue
		}
		// Walk back to the line that starts the command, then forward
		// through its continuations.
		start := i
		for start > 0 && strings.HasSuffix(strings.TrimSpace(lines[start-1]), "\\") {
			start--
		}
		var command []string
		for j := start; j < len(lines); j++ {
			piece := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(lines[j]), "\\"))
			command = append(command, piece)
			if !strings.HasSuffix(strings.TrimSpace(lines[j]), "\\") {
				break
			}
		}
		return strings.Join(command, " ")
	}
	t.Fatal("the CI workflow has no go test command using $SHARD_RUN: the shard job selects " +
		"tests some other way and this guard is enforcing nothing")
	return ""
}

// shardMatrix extracts the shard ids the workflow's matrix lists, in order.
func shardMatrix(t *testing.T, workflow string) []string {
	t.Helper()
	const marker = "shard: ["
	index := strings.Index(workflow, marker)
	if index < 0 {
		t.Fatal("the CI workflow has no shard matrix: the sharded job is gone, or it enumerates " +
			"shards some other way and this guard is enforcing nothing")
	}
	rest := workflow[index+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatal("the CI workflow's shard matrix is not closed")
	}
	var ids []string
	for _, field := range strings.Split(rest[:end], ",") {
		ids = append(ids, strings.TrimSpace(field))
	}
	return ids
}

// Restore's destructive recovery-state clear must sit INSIDE D4's phase
// boundary — after the incomplete marker is written, never before it.
//
// D4's rule is that every deletion is preceded by a durable record that a
// destructive operation began, so a crash leaves a torn tree that announces
// itself. An earlier version cleared the recovery marker and staged key in
// Restore, before replaceTree wrote anything: a kill in that window deleted
// recovery state with nothing on disk saying so, reopening the exact window
// the marker exists to close, one call above it.
//
// Checked structurally because the defect is only observable through a crash
// in a window of a few milliseconds. A behavioural test cannot reach it, and
// both spellings look equally careful in review — which is how it got
// written.
func TestRestoreClearsRecoveryStateOnlyAfterItsMarker(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "restore.go", nil, 0)
	if err != nil {
		t.Fatalf("parse restore.go: %v", err)
	}

	bodies := map[string]*ast.BlockStmt{}
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Recv == nil {
			bodies[fn.Name.Name] = fn.Body
		}
	}

	// The DESTRUCTIVE clear must not appear in Restore at all: everything
	// there runs before replaceTree writes the marker.
	restore, ok := bodies["Restore"]
	if !ok {
		t.Fatal("Restore is gone: this guard is enforcing nothing")
	}
	for _, banned := range []string{"clearRecoveryState", "clearRecoveryResidue"} {
		if callsNamed(restore, banned) {
			t.Errorf("Restore calls %s, which deletes durable recovery state, before replaceTree "+
				"writes RestoreIncompleteMarker: a crash there destroys state with nothing on disk "+
				"recording that a destructive operation began", banned)
		}
	}
	// It must stop the container, though — that has to happen before the
	// data root is cleared, and destroys nothing that cannot be rebuilt.
	if !callsNamed(restore, "stopRecoveryContainer") {
		t.Error("Restore never stops the recovery container: it would clear the data root while an " +
			"orphaned postmaster still held it open")
	}

	// And inside replaceTree, the clear must follow the marker write.
	replaceTree, ok := bodies["replaceTree"]
	if !ok {
		t.Fatal("replaceTree is gone: this guard is enforcing nothing")
	}
	markerAt, clearAt := -1, -1
	for i, statement := range replaceTree.List {
		ast.Inspect(statement, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			name, isIdent := call.Fun.(*ast.Ident)
			if !isIdent {
				return true
			}
			if name.Name == "writeRestoreMarker" && markerAt < 0 {
				markerAt = i
			}
			if name.Name == "clearRecoveryState" && clearAt < 0 {
				clearAt = i
			}
			return true
		})
	}
	switch {
	case markerAt < 0:
		t.Fatal("replaceTree never writes the restore marker: this guard is enforcing nothing")
	case clearAt < 0:
		t.Error("replaceTree never clears recovery state: an interrupted recovery's marker and " +
			"staged key would survive a restore, gating every later verb on a plane that was " +
			"just replaced")
	case clearAt < markerAt:
		t.Errorf("recovery state is cleared at statement %d but the restore marker is written at "+
			"%d: the deletion precedes the record that a destructive operation began", clearAt, markerAt)
	}
}

// callsNamed reports whether a block calls a named package-level function.
func callsNamed(body *ast.BlockStmt, target string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if name, isIdent := call.Fun.(*ast.Ident); isIdent && name.Name == target {
			found = true
			return false
		}
		return true
	})
	return found
}
