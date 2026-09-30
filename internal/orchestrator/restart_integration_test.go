//go:build integration

package orchestrator_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	planeharness "orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
	"orchestrator/internal/orchestrator"
)

// The restart proof (design D13): a test-only subprocess commits work and
// exits or is killed; a FRESH process reconstructs from the plane alone.
//
// Constructing a second Orchestrator in the same process proves nothing --
// package state, caches and pools survive. The child is this test binary
// re-executed with a role in the environment: `commit` provisions and
// dispatches, `recover` starts an Orchestrator and prints its projection as
// JSON. The parent plays the "other writer" between the two: it applies one
// change and asserts the fresh process classifies it, for the named reason.

const (
	roleEnv    = "MAESTRO_RESTART_ROLE"
	dsnEnv     = "MAESTRO_RESTART_DSN"
	objectsEnv = "MAESTRO_RESTART_OBJECTS"
	orgSlug    = "acme"
	operator   = "dan"
	// committedLine is what the commit role prints once its writes are
	// durable, so a parent that kills it knows the kill lands AFTER commit.
	committedLine = "COMMITTED"
)

// fixedRootKey is the vault key both processes use. No secret is written,
// so its value is irrelevant; it must simply be the same in both.
func fixedRootKey(t testing.TB) secret.RootKeyProvider {
	t.Helper()
	provider, err := secret.ResolvedKey([]byte(strings.Repeat("k", secret.RootKeyLen)), secret.BackendOperatorProvided)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// TestMain routes a re-executed child to its role. An ordinary test run
// has no role and proceeds normally.
func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "":
		os.Exit(m.Run())
	case "commit":
		os.Exit(childCommit())
	case "recover":
		os.Exit(childRecover())
	default:
		fmt.Fprintln(os.Stderr, "unknown role", os.Getenv(roleEnv))
		os.Exit(2)
	}
}

// childOpener rebuilds the seam from the environment, with nothing
// inherited from the parent process but strings.
func childOpener() (orchestrator.Opener, error) {
	var objectCfg objects.Config
	if err := json.Unmarshal([]byte(os.Getenv(objectsEnv)), &objectCfg); err != nil {
		return nil, fmt.Errorf("object config: %w", err)
	}
	blob, err := objects.New(objectCfg)
	if err != nil {
		return nil, err
	}
	types, err := orchestrator.Registry()
	if err != nil {
		return nil, err
	}
	rootKey, err := secret.ResolvedKey([]byte(strings.Repeat("k", secret.RootKeyLen)), secret.BackendOperatorProvided)
	if err != nil {
		return nil, err
	}
	// The same version the parent composes with: a child that ran under
	// another would be a second harness restarting the first one's work.
	running, err := planeharness.Parse(planetest.HarnessVersion)
	if err != nil {
		return nil, err
	}
	caller := plane.Caller{Types: types, Keys: orchestrator.Keys(), Prompts: orchestrator.Prompts(), Harness: running}
	dsn := os.Getenv(dsnEnv)
	return func(ctx context.Context) (store.Store, error) {
		return plane.Open(ctx, plane.Composition{DSN: dsn, Objects: blob, RootKey: rootKey, Caller: caller})
	}, nil
}

// childCommit provisions a governed hierarchy with two completed
// predecessors, dispatches, optionally accepts, prints COMMITTED, then waits
// to be killed or told to exit.
func childCommit() int {
	ctx := context.Background()
	open, err := childOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	seam, err := open(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer seam.Close()
	ids, err := commitWork(ctx, seam, os.Getenv("MAESTRO_RESTART_ACCEPT") == "1")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	encoded, _ := json.Marshal(ids)
	fmt.Println(string(encoded))
	fmt.Println(committedLine)
	// Hold until the parent decides how this process ends.
	time.Sleep(time.Hour)
	return 0
}

// committed is what the commit child hands back: every id the parent needs
// to play the other writer.
type committed struct {
	Organization uuid.UUID   `json:"organization"`
	User         uuid.UUID   `json:"user"`
	Product      uuid.UUID   `json:"product"`
	Feature      uuid.UUID   `json:"feature"`
	Epic         uuid.UUID   `json:"epic"`
	Story        uuid.UUID   `json:"story"`
	StoryRecord  uuid.UUID   `json:"story_record"`
	EpicRecord   uuid.UUID   `json:"epic_record"`
	Predecessors []uuid.UUID `json:"predecessors"`
	Completions  []uuid.UUID `json:"completions"`
	Dispatch     uuid.UUID   `json:"dispatch"`
	Author       uuid.UUID   `json:"author"`
	Reviewer     uuid.UUID   `json:"reviewer"`
	// Resolution is the prompt-pack decision the dispatch persisted (item 4,
	// ADR 0031 §5): what the fresh process must read back verbatim.
	Resolution resolutionJSON `json:"resolution"`
}

// resolutionJSON is the part of a store.PromptResolution that identifies
// the decision: which content, which installation record at which revision,
// under which validated harness version. Comparable, so the parent asserts
// equality rather than picking fields.
type resolutionJSON struct {
	ResolutionID         string `json:"resolution_id"`
	Scheme               string `json:"scheme"`
	Digest               string `json:"digest"`
	ContentID            string `json:"content_id"`
	InstallationID       string `json:"installation_id"`
	InstallationRevision int    `json:"installation_revision"`
	ResolvedName         string `json:"resolved_name"`
	DisplayName          string `json:"display_name"`
	ValidatedVersion     string `json:"validated_version"`
}

func resolutionOf(r *store.PromptResolution) resolutionJSON {
	return resolutionJSON{
		ResolutionID: r.ResolutionID.String(), Scheme: string(r.Identity.Scheme), Digest: r.Identity.Digest,
		ContentID: r.ContentID.String(), InstallationID: r.InstallationID.String(),
		InstallationRevision: r.InstallationRevision, ResolvedName: r.ResolvedName,
		DisplayName: r.Snapshot.DisplayName, ValidatedVersion: r.ValidatedMaestroVersion,
	}
}

func commitWork(ctx context.Context, seam store.Store, accept bool) (committed, error) {
	var ids committed
	org, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{Slug: orgSlug, DisplayName: "Acme"})
	if err != nil {
		return ids, err
	}
	ids.Organization = org.Record.OrganizationID
	user, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{Handle: operator, DisplayName: "Dan", OrganizationID: ids.Organization})
	if err != nil {
		return ids, err
	}
	ids.User = user.Record.UserID
	for _, p := range []struct {
		out  *uuid.UUID
		name string
	}{{&ids.Author, "author"}, {&ids.Reviewer, "reviewer"}} {
		// These principals only author and review artifacts, so they are
		// recorded as foreign imports with closed lifetimes: the general path
		// admits no agent, and a live agent exists only under an execution
		// (item 4 design, D5). The harness's dispatches are created below
		// with no principal starting under them.
		instance, err := seam.RecordForeignAgentPrincipal(ctx, store.RecordForeignAgentPrincipalInput{
			Model: "restart-" + p.name, AgentType: "restart-harness", OrganizationID: ids.Organization,
			Pack:     store.ForeignPromptPack{Name: "fixture", Digest: "sha256:" + strings.Repeat("a", 64)},
			Lifetime: store.RecordedLifetime{StartTime: time.Now().Add(-time.Hour), StopTime: time.Now(), StopReason: "fixture"},
		})
		if err != nil {
			return ids, fmt.Errorf("principal %s: %w", p.name, err)
		}
		*p.out = instance.PrincipalInstanceID
	}
	product, err := seam.ProvisionProduct(ctx, store.ProvisionProductInput{Slug: "core", DisplayName: "Core", OrganizationID: ids.Organization, UserID: ids.User})
	if err != nil {
		return ids, err
	}
	ids.Product = product.Record.ProductID
	repo, err := seam.ProvisionRepository(ctx, store.ProvisionRepositoryInput{Slug: "api", DisplayName: "API", OrganizationID: ids.Organization, PrimaryProductID: ids.Product, UserID: ids.User})
	if err != nil {
		return ids, err
	}
	// Since item 4 a dispatch resolves a prompt pack. The child's seam is
	// composed the way the real root composes it -- the Orchestrator's key
	// vocabulary and its empty slot registry -- so the empty pack is what
	// installs and what the organization selects (design D8, D9).
	if err := seedPromptPack(ctx, seam, ids.Organization); err != nil {
		return ids, err
	}
	feature, err := seam.CreateFeature(ctx, store.CreateFeatureInput{Title: "Flags", OrganizationID: ids.Organization, UserID: ids.User, ProductID: ids.Product})
	if err != nil {
		return ids, err
	}
	ids.Feature = feature.FeatureID
	epic, err := seam.CreateEpic(ctx, store.CreateEpicInput{Title: "Instance flag", OrganizationID: ids.Organization, UserID: ids.User, FeatureID: ids.Feature, RepositoryID: repo.Record.RepositoryID})
	if err != nil {
		return ids, err
	}
	ids.Epic = epic.EpicID
	story, err := seam.CreateStory(ctx, store.CreateStoryInput{Title: "Add the flag", OrganizationID: ids.Organization, UserID: ids.User, EpicID: ids.Epic})
	if err != nil {
		return ids, err
	}
	ids.Story = story.StoryID
	lineage := func(storyID *uuid.UUID) store.Lineage {
		return store.Lineage{ProductID: &ids.Product, FeatureID: &ids.Feature, EpicID: &ids.Epic, StoryID: storyID}
	}
	accepted := func(kind registry.Type, scope store.Scope, l store.Lineage, payload string) (uuid.UUID, error) {
		return acceptRecord(ctx, seam, &ids, kind, scope, l, payload)
	}
	if ids.EpicRecord, err = accepted(work.TypeEpicRecord, store.Scope{Type: store.ScopeEpic, ID: ids.Epic}, lineage(nil), `{"intent":"flags","mode":"factory"}`); err != nil {
		return ids, err
	}
	if ids.StoryRecord, err = accepted(work.TypeStoryRecord, store.Scope{Type: store.ScopeStory, ID: ids.Story}, lineage(&ids.Story), `{"intent":"add the flag"}`); err != nil {
		return ids, err
	}
	if err := seam.SetEpicGoverningArtifact(ctx, ids.Organization, ids.Epic, ids.EpicRecord); err != nil {
		return ids, err
	}
	if err := seam.SetStoryGoverningArtifact(ctx, ids.Organization, ids.Story, ids.StoryRecord); err != nil {
		return ids, err
	}
	if _, err := seam.EnsureWorkGroup(ctx, ids.Organization, ids.Epic); err != nil {
		return ids, err
	}
	// Two completed predecessors. The edges and satisfying pointers are
	// item 10's writers; planted through the seam's transaction here.
	for _, title := range []string{"one", "two"} {
		predecessor, err := seam.CreateStory(ctx, store.CreateStoryInput{Title: title, OrganizationID: ids.Organization, UserID: ids.User, EpicID: ids.Epic})
		if err != nil {
			return ids, err
		}
		completion, err := accepted(work.TypeStoryCompletion, store.Scope{Type: store.ScopeStory, ID: predecessor.StoryID}, lineage(&predecessor.StoryID),
			`{"head_commit":"0123456789abcdef0123456789abcdef01234567"}`)
		if err != nil {
			return ids, err
		}
		if err := plantEdge(ctx, os.Getenv(dsnEnv), &ids, predecessor.StoryID, completion); err != nil {
			return ids, err
		}
		ids.Predecessors = append(ids.Predecessors, predecessor.StoryID)
		ids.Completions = append(ids.Completions, completion)
	}
	dispatch, err := seam.CreateDispatch(ctx, ids.Organization, ids.Story, nil)
	if err != nil {
		return ids, err
	}
	ids.Dispatch = dispatch.StoryDispatchID
	ids.Resolution = resolutionOf(&dispatch.PromptResolution)
	if accept {
		if _, err := seam.AcceptDispatch(ctx, ids.Organization, ids.Dispatch, store.ExecutionConfiguration{
			CapabilitySet: []string{}, ActingUserID: ids.User,
		}); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func acceptRecord(ctx context.Context, seam store.Store, ids *committed, kind registry.Type, scope store.Scope, lineage store.Lineage, payload string) (uuid.UUID, error) {
	draft, err := seam.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload: json.RawMessage(payload), Type: kind, Summary: string(kind), Scope: scope, Lineage: lineage,
		OrganizationID: ids.Organization, UserID: ids.User, AuthorInstanceID: ids.Author,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create %s: %w", kind, err)
	}
	review, err := seam.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest: draft.ReviewDigest, Rationale: "ok", Decision: store.DecisionAccepted,
		OrganizationID: ids.Organization, ArtifactID: draft.ArtifactID, ReviewerInstanceID: ids.Reviewer,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("review %s: %w", kind, err)
	}
	if err := seam.AcceptArtifact(ctx, ids.Organization, draft.ArtifactID, review.ReviewID); err != nil {
		return uuid.Nil, fmt.Errorf("accept %s: %w", kind, err)
	}
	return draft.ArtifactID, nil
}

// plantEdge writes a satisfied predecessor edge. There is no seam writer for
// edges until item 10, so the fixture reaches the table directly, as item
// 2's tests do; nothing in production has this path.
func plantEdge(ctx context.Context, dsn string, ids *committed, predecessor, completion uuid.UUID) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO story_dependencies
		(organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id, satisfying_completion_artifact_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ids.Organization, ids.Product, ids.Feature, ids.Epic, ids.Story, predecessor, completion)
	return err
}

// projected is what the recover child prints.
type projected struct {
	Counts map[string]int     `json:"counts"`
	Rows   map[string]rowJSON `json:"rows"`
}

type rowJSON struct {
	Class       string         `json:"class"`
	Component   string         `json:"component,omitempty"`
	Predecessor string         `json:"predecessor,omitempty"`
	Resolution  resolutionJSON `json:"resolution"`
}

func childRecover() int {
	open, err := childOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	o, err := orchestrator.Start(context.Background(), open, orchestrator.Config{OrganizationSlug: orgSlug, OperatorHandle: operator})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer o.Close()
	p := o.Projection()
	out := projected{Counts: map[string]int{}, Rows: map[string]rowJSON{}}
	for class, n := range p.Counts {
		out.Counts[string(class)] = n
	}
	for _, row := range p.Rows {
		r := rowJSON{Class: string(row.Class)}
		// The resolution as this process reads it: from the plane, through
		// the seam the Orchestrator started with, and nothing else.
		dispatch, err := o.Store().GetDispatch(context.Background(), o.Organization().OrganizationID, row.DispatchID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		r.Resolution = resolutionOf(&dispatch.PromptResolution)
		if row.Divergence != nil {
			r.Component = string(row.Divergence.Component)
			if row.Divergence.Predecessor != uuid.Nil {
				r.Predecessor = row.Divergence.Predecessor.String()
			}
		}
		out.Rows[row.DispatchID.String()] = r
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
	return 0
}

// --- the parent ---------------------------------------------------------

type harness struct {
	t      *testing.T
	dsn    string
	env    []string
	seam   store.Store
	ids    committed
	objCfg objects.Config
}

func newHarness(t *testing.T, accept bool, kill bool) *harness {
	t.Helper()
	dsn := planetest.DSN(t, "restart")
	blob, objCfg := planetest.Blob(t, "restart")
	encoded, err := json.Marshal(objCfg)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dsn: dsn, objCfg: objCfg}
	h.env = append(os.Environ(), dsnEnv+"="+dsn, objectsEnv+"="+string(encoded))
	if accept {
		h.env = append(h.env, "MAESTRO_RESTART_ACCEPT=1")
	}
	// The parent's own seam, to play the other writer.
	types, err := orchestrator.Registry()
	if err != nil {
		t.Fatal(err)
	}
	h.seam, err = plane.Open(context.Background(), plane.Composition{
		DSN: dsn, Objects: blob, RootKey: fixedRootKey(t),
		Caller: plane.Caller{Types: types, Keys: orchestrator.Keys(), Prompts: orchestrator.Prompts(), Harness: planetest.Harness(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.seam.Close)
	h.ids = h.runCommit(kill)
	return h
}

// runCommit runs the commit child, waits for COMMITTED, then either kills
// it (SIGKILL: no deferred close, no flush) or lets it be interrupted.
func (h *harness) runCommit(kill bool) committed {
	t := h.t
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^$")
	cmd.Env = append(h.env, roleEnv+"=commit")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	var ids committed
	committedSeen := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == committedLine {
			committedSeen = true
			break
		}
		if err := json.Unmarshal([]byte(line), &ids); err != nil {
			t.Fatalf("child output %q: %v", line, err)
		}
	}
	if !committedSeen {
		_ = cmd.Process.Kill()
		t.Fatal("the commit child never reported COMMITTED")
	}
	signal := syscall.SIGTERM
	if kill {
		signal = syscall.SIGKILL
	}
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	return ids
}

// recover runs a FRESH process and returns its projection.
func (h *harness) recover() projected {
	t := h.t
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^$")
	cmd.Env = append(h.env, roleEnv+"=recover")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("recover child: %v", err)
	}
	var p projected
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("recover output %q: %v", out, err)
	}
	return p
}

func (h *harness) expect(p projected, class orchestrator.Class, component orchestrator.Component, predecessor uuid.UUID) {
	t := h.t
	t.Helper()
	row, ok := p.Rows[h.ids.Dispatch.String()]
	if !ok {
		t.Fatalf("the fresh process did not see dispatch %s: %+v", h.ids.Dispatch, p)
	}
	if row.Class != string(class) {
		t.Fatalf("class %s, want %s (%+v)", row.Class, class, row)
	}
	if row.Component != string(component) {
		t.Fatalf("component %q, want %q (%+v)", row.Component, component, row)
	}
	if predecessor != uuid.Nil && row.Predecessor != predecessor.String() {
		t.Fatalf("predecessor %s, want %s", row.Predecessor, predecessor)
	}
	// ADR 0031 §5: resolved once at dispatch, reused verbatim across
	// restarts. The fresh process read the decision the commit child
	// persisted -- every identifying field, not a re-resolution.
	if row.Resolution != h.ids.Resolution {
		t.Fatalf("the fresh process read resolution\n  %+v\nbut the dispatch persisted\n  %+v", row.Resolution, h.ids.Resolution)
	}
	if row.Resolution.ResolutionID == "" || row.Resolution.Digest == "" {
		t.Fatalf("an empty resolution survived the comparison: %+v", row.Resolution)
	}
	if len(p.Rows) != 1 {
		t.Fatalf("%d rows, want 1", len(p.Rows))
	}
}

// amendNoOp accepts a byte-identical amendment of an original, as the other
// writer.
func (h *harness) amendNoOp(original uuid.UUID, kind registry.Type, scope store.Scope, lineage store.Lineage) {
	t := h.t
	t.Helper()
	ctx := context.Background()
	base, err := h.seam.AmendmentBase(ctx, h.ids.Organization, original)
	if err != nil {
		t.Fatal(err)
	}
	amendment, err := h.seam.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload: json.RawMessage(`{}`), Type: kind, Summary: "no-op", Scope: scope, Lineage: lineage,
		AmendsArtifactID: &original, OrganizationID: h.ids.Organization, UserID: h.ids.User, AuthorInstanceID: h.ids.Author,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, sequence := base.Digest, base.Sequence
	review, err := h.seam.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest: amendment.ReviewDigest, Rationale: "no-op", Decision: store.DecisionAccepted,
		BaseDigest: &digest, BaseSequence: &sequence,
		OrganizationID: h.ids.Organization, ArtifactID: amendment.ArtifactID, ReviewerInstanceID: h.ids.Reviewer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.seam.AcceptAmendment(ctx, h.ids.Organization, amendment.ArtifactID, review.ReviewID); err != nil {
		t.Fatal(err)
	}
}

// moveInstallation corrects the installation the dispatch resolved, as the
// other writer: a new display name at revision 2. The dispatch's resolution
// is history and must not follow it.
func (h *harness) moveInstallation() {
	t := h.t
	t.Helper()
	ctx := context.Background()
	pack, err := h.seam.GetPromptPackByIdentity(ctx, h.ids.Organization, store.PromptIdentity{Scheme: store.PromptScheme(h.ids.Resolution.Scheme), Digest: h.ids.Resolution.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if pack.Installation.Revision != h.ids.Resolution.InstallationRevision {
		t.Fatalf("installation at revision %d, the dispatch resolved %d: the fixture moved before the test did", pack.Installation.Revision, h.ids.Resolution.InstallationRevision)
	}
	if _, err := h.seam.UpdatePromptPackInstallation(ctx, store.UpdatePromptPackInstallationInput{
		DisplayName: "moved after dispatch", MinMaestroVersion: pack.Installation.MinMaestroVersion, MaxMaestroVersion: pack.Installation.MaxMaestroVersion,
		ExpectedRevision: pack.Installation.Revision, OrganizationID: h.ids.Organization, InstallationID: pack.Installation.InstallationID,
	}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) lineage(storyID *uuid.UUID) store.Lineage {
	return store.Lineage{ProductID: &h.ids.Product, FeatureID: &h.ids.Feature, EpicID: &h.ids.Epic, StoryID: storyID}
}

// TestRestartRecoversCommittedWork: the baseline, with the child KILLED
// after commit -- a clean exit can flush something a kill would not.
func TestRestartRecoversCommittedWork(t *testing.T) {
	h := newHarness(t, false, true)
	// Between the two processes the installation record moves on: a
	// re-resolution in the fresh process would read revision 2 and the new
	// display name, and expect() would see it. THE MUTANT: have the recover
	// child resolve afresh instead of reading the dispatch's row.
	h.moveInstallation()
	p := h.recover()
	h.expect(p, orchestrator.PendingResumable, "", uuid.Nil)
	if p.Counts[string(orchestrator.PendingResumable)] != 1 {
		t.Fatalf("counts %+v", p.Counts)
	}
}

// TestRestartRecoversAnAcceptedDispatch: the execution item 3 creates lands
// awaiting the boundary.
func TestRestartRecoversAnAcceptedDispatch(t *testing.T) {
	h := newHarness(t, true, true)
	h.expect(h.recover(), orchestrator.ExecutionAwaitingBoundary, "", uuid.Nil)
}

// TestRestartClassifiesEachTransitionShape: five representative shapes,
// each a single change by another writer between the two processes, each
// landing pending_diverged for its own reason. Representative, not the
// inventory: item 2's nine transitions are item 9's, and the comparator's
// own coverage is the unit test's.
func TestRestartClassifiesEachTransitionShape(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		change    func(h *harness) uuid.UUID
		component orchestrator.Component
	}{
		{"no-op story amendment", func(h *harness) uuid.UUID {
			h.amendNoOp(h.ids.StoryRecord, work.TypeStoryRecord, store.Scope{Type: store.ScopeStory, ID: h.ids.Story}, h.lineage(&h.ids.Story))
			return uuid.Nil
		}, orchestrator.StorySequence},
		{"story repoint to an identical twin", func(h *harness) uuid.UUID {
			twin, err := acceptRecord(ctx, h.seam, &h.ids, work.TypeStoryRecord, store.Scope{Type: store.ScopeStory, ID: h.ids.Story}, h.lineage(&h.ids.Story), `{"intent":"add the flag"}`)
			if err != nil {
				h.t.Fatal(err)
			}
			if err := h.seam.SetStoryGoverningArtifact(ctx, h.ids.Organization, h.ids.Story, twin); err != nil {
				h.t.Fatal(err)
			}
			return uuid.Nil
		}, orchestrator.StoryID},
		{"no-op epic amendment", func(h *harness) uuid.UUID {
			h.amendNoOp(h.ids.EpicRecord, work.TypeEpicRecord, store.Scope{Type: store.ScopeEpic, ID: h.ids.Epic}, h.lineage(nil))
			return uuid.Nil
		}, orchestrator.EpicSequence},
		{"added already-satisfied predecessor", func(h *harness) uuid.UUID {
			extra, err := h.seam.CreateStory(ctx, store.CreateStoryInput{Title: "three", OrganizationID: h.ids.Organization, UserID: h.ids.User, EpicID: h.ids.Epic})
			if err != nil {
				h.t.Fatal(err)
			}
			completion, err := acceptRecord(ctx, h.seam, &h.ids, work.TypeStoryCompletion, store.Scope{Type: store.ScopeStory, ID: extra.StoryID}, h.lineage(&extra.StoryID),
				`{"head_commit":"0123456789abcdef0123456789abcdef01234567"}`)
			if err != nil {
				h.t.Fatal(err)
			}
			if err := plantEdge(ctx, h.dsn, &h.ids, extra.StoryID, completion); err != nil {
				h.t.Fatal(err)
			}
			return extra.StoryID
		}, orchestrator.EdgeSet},
		{"no-op amendment of the second predecessor's completion", func(h *harness) uuid.UUID {
			p := h.ids.Predecessors[1]
			h.amendNoOp(h.ids.Completions[1], work.TypeStoryCompletion, store.Scope{Type: store.ScopeStory, ID: p}, h.lineage(&p))
			return p
		}, orchestrator.CompletionSequence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, false, false)
			predecessor := tc.change(h)
			h.expect(h.recover(), orchestrator.PendingDiverged, tc.component, predecessor)
		})
	}
}

// seedPromptPack provisions the organization's prompt pack as the
// composition root does: the EMBEDDED built-in through LoadBuiltin, then
// the seam's provisioning verb (design D2, D9). Idempotent.
func seedPromptPack(ctx context.Context, seam store.Store, organization uuid.UUID) error {
	builtin, err := orchestrator.LoadBuiltin(orchestrator.BuiltinPack())
	if err != nil {
		return err
	}
	if _, err := seam.ProvisionOrganizationPromptPack(ctx, organization, builtin); err != nil {
		return fmt.Errorf("provision the built-in pack: %w", err)
	}
	return nil
}
