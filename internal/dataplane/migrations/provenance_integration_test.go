//go:build integration

package migrations_test

import (
	"strings"
	"testing"
)

// ADR 0022's guardrail -- every state-changing LLM output passes through a
// tool/action record -- is only DEMONSTRABLE if the chain is recorded. These
// cover the links that make it auditable, and the previous commit claimed
// they were tested when they were not.

func (f *fixture) insertLLMCall(id, org string) error {
	_, err := f.tx.Exec(
		`INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model)
		 VALUES ($1,$2,$3,'anthropic','opus')`,
		id, org, f.principal)
	return err
}

func (f *fixture) insertToolCall(id, org string, llmCallID any) error {
	_, err := f.tx.Exec(
		`INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, llm_call_id, tool_name, arguments)
		 VALUES ($1,$2,$3,$4,'write_file','{}')`,
		id, org, f.principal, llmCallID)
	return err
}

func TestToolCallLinksToItsOriginatingLLMCall(t *testing.T) {
	f := seed(t, openPlane(t))

	llmCall := "50000000-0000-7000-8000-000000000001"
	if err := f.insertLLMCall(llmCall, f.org); err != nil {
		t.Fatalf("insert llm call: %v", err)
	}
	if err := f.insertToolCall("50000000-0000-7000-8000-000000000002", f.org, llmCall); err != nil {
		t.Fatalf("tool call could not reference its LLM call: %v", err)
	}

	// Orchestrator-initiated tool calls have no LLM parent, and recording
	// null is honest where inventing one would not be.
	if err := f.insertToolCall("50000000-0000-7000-8000-000000000003", f.org, nil); err != nil {
		t.Errorf("a tool call with no originating LLM call was rejected: %v", err)
	}
}

func TestArtifactLinksToTheToolCallThatProducedIt(t *testing.T) {
	f := seed(t, openPlane(t))

	toolCall := "50000000-0000-7000-8000-000000000004"
	if err := f.insertToolCall(toolCall, f.org, nil); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if err := f.insertStoryArtifact("50000000-0000-7000-8000-000000000005",
		map[string]any{"produced_by_tool_call_id": toolCall}); err != nil {
		t.Fatalf("artifact could not reference its producing tool call: %v", err)
	}
}

func TestProvenanceLinksCannotCrossOrganizations(t *testing.T) {
	f := seed(t, openPlane(t))

	otherOrg := "50000000-0000-7000-8000-00000000000a"
	otherUser := "50000000-0000-7000-8000-00000000000b"
	otherPrincipal := "50000000-0000-7000-8000-00000000000c"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'o9','O9')`, []any{otherOrg}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u9','U9')`, []any{otherUser, otherOrg}},
		{`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		  VALUES ($1,$2,'agent','opus','coder')`, []any{otherPrincipal, otherOrg}},
	} {
		if _, err := f.tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// A tool call in the other organization, which this organization's
	// artifact must not be able to claim as its provenance.
	if _, err := f.tx.Exec(
		`INSERT INTO tool_calls (tool_call_id, organization_id, principal_instance_id, tool_name, arguments)
		 VALUES ($1,$2,$3,'write_file','{}')`,
		"50000000-0000-7000-8000-00000000000d", otherOrg, otherPrincipal); err != nil {
		t.Fatalf("seed foreign tool call: %v", err)
	}

	err := f.insertStoryArtifact("50000000-0000-7000-8000-00000000000e",
		map[string]any{"produced_by_tool_call_id": "50000000-0000-7000-8000-00000000000d"})
	if err == nil {
		t.Fatal("an artifact claimed provenance from another organization's tool call")
	}
}

// The primary-Product-is-a-member rule is DEFERRED, so it fires at commit
// rather than at statement time. A test that only checked the insert would
// see it pass and conclude the constraint does not work.
func TestPrimaryProductMustBeAMemberAtCommit(t *testing.T) {
	db := openPlane(t)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	org := "60000000-0000-7000-8000-000000000001"
	user := "60000000-0000-7000-8000-000000000002"
	product := "60000000-0000-7000-8000-000000000003"
	repo := "60000000-0000-7000-8000-000000000004"

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'od','OD')`, []any{org}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'ud','UD')`, []any{user, org}},
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'pd','PD')`, []any{product, org, user}},
		// Repository names a primary Product but no membership row is added.
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,$4,'rd','RD')`, []any{repo, org, product, user}},
	} {
		if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("statement should have been accepted (the check is deferred): %v", err)
		}
	}

	err = tx.Commit()
	if err == nil {
		t.Fatal("committed a repository whose primary Product is not a member")
	}
	if !strings.Contains(err.Error(), "primary_is_member") {
		t.Errorf("failed on the wrong constraint: %v", err)
	}
}

// foreignOrg seeds a second organization with its own user and principal,
// for the cross-organization rejection cases below.
type foreign struct{ org, user, principal string }

func seedForeignOrg(t *testing.T, f *fixture, suffix string) foreign {
	t.Helper()
	fo := foreign{
		org:       "70000000-0000-7000-8000-0000000000" + suffix + "1",
		user:      "70000000-0000-7000-8000-0000000000" + suffix + "2",
		principal: "70000000-0000-7000-8000-0000000000" + suffix + "3",
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,$2,'X')`, []any{fo.org, "org" + suffix}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'fu','FU')`, []any{fo.user, fo.org}},
		{`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		  VALUES ($1,$2,'agent','opus','coder')`, []any{fo.principal, fo.org}},
	} {
		if _, err := f.tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed foreign org: %v", err)
		}
	}
	return fo
}

// Lifecycle links referencing artifact_id alone would let one tenant's
// artifact amend or supersede another's, quietly joining two histories.
func TestLifecycleLinksCannotCrossOrganizations(t *testing.T) {
	for _, link := range []string{"amends_artifact_id", "supersedes_artifact_id", "replaces_artifact_id"} {
		t.Run(link, func(t *testing.T) {
			f := seed(t, openPlane(t))
			fo := seedForeignOrg(t, f, "a")

			// A well-formed artifact in the OTHER organization.
			foreignArtifact := "70000000-0000-7000-8000-0000000000a4"
			if _, err := f.tx.Exec(
				`INSERT INTO management_artifacts
				  (artifact_id, organization_id, user_id, artifact_type, scope_type,
				   scope_organization_id, author_instance_id, schema_version, summary,
				   payload, payload_digest, review_digest)
				 VALUES ($1,$2,$3,'plan','organization',$2,$4,1,'s','{}',$5,$6)`,
				foreignArtifact, fo.org, fo.user, fo.principal, digestA, digestB); err != nil {
				t.Fatalf("seed foreign artifact: %v", err)
			}

			overrides := map[string]any{link: foreignArtifact}
			if link == "amends_artifact_id" {
				overrides["status"] = "accepted"
				overrides["accepted_at"] = "2026-01-01T00:00:00Z"
				overrides["amendment_sequence"] = 1
			}

			if err := f.insertStoryArtifact("70000000-0000-7000-8000-0000000000a5", overrides); err == nil {
				t.Fatalf("%s referenced an artifact in another organization", link)
			}
		})
	}
}

// An Epic owns a branch, so its repository must belong to its Product --
// not merely to the same organization.
func TestEpicCannotUseANonMemberRepository(t *testing.T) {
	f := seed(t, openPlane(t))

	// A second Product and a repository that belongs only to it.
	otherProduct := "70000000-0000-7000-8000-0000000000b1"
	otherRepo := "70000000-0000-7000-8000-0000000000b2"
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'p2','P2')`,
			[]any{otherProduct, f.org, f.user}},
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,$4,'r2','R2')`, []any{otherRepo, f.org, otherProduct, f.user}},
		{`INSERT INTO product_repositories (product_id, repository_id, organization_id) VALUES ($1,$2,$3)`,
			[]any{otherProduct, otherRepo, f.org}},
	} {
		if _, err := f.tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// The Epic's Product is f.product; the repository belongs to otherProduct.
	_, err := f.tx.Exec(
		`INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
		 VALUES ($1,$2,$3,$4,$5,$6,'E2')`,
		"70000000-0000-7000-8000-0000000000b3", f.org, f.user, f.product, f.feature, otherRepo)
	if err == nil {
		t.Fatal("an Epic claimed a repository that is not a member of its Product")
	}
	if !strings.Contains(err.Error(), "repository_membership") {
		t.Errorf("failed on the wrong constraint: %v", err)
	}
}

// Cost analysis groups by exactly these columns, so an inconsistent tuple
// silently misattributes spend.
//
// Two separate fixtures rather than two inserts in one: a rejected
// statement aborts the surrounding transaction, so the second attempt would
// fail with "transaction is aborted" and prove nothing about its own
// constraint.
func TestCallLineageMustReferenceARealTuple(t *testing.T) {
	f := seed(t, openPlane(t))

	otherFeature := "70000000-0000-7000-8000-0000000000c1"
	if _, err := f.tx.Exec(
		`INSERT INTO features (feature_id, organization_id, user_id, product_id, title) VALUES ($1,$2,$3,$4,'F3')`,
		otherFeature, f.org, f.user, f.product); err != nil {
		t.Fatalf("seed feature: %v", err)
	}

	// The Story belongs to f.feature; this call claims otherFeature.
	_, err := f.tx.Exec(
		`INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id,
		                        product_id, feature_id, epic_id, story_id, provider, model)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'anthropic','opus')`,
		"70000000-0000-7000-8000-0000000000c2", f.org, f.principal,
		f.product, otherFeature, f.epic, f.story)
	if err == nil {
		t.Fatal("an LLM call carried a lineage tuple that does not exist")
	}
}

// A partially-filled tuple must also be refused: MATCH SIMPLE skips a
// composite foreign key entirely when any column is null, so the shape
// CHECK is the only thing standing between a half-filled lineage and the
// database.
func TestCallLineageMustBeFilledTopDown(t *testing.T) {
	f := seed(t, openPlane(t))

	_, err := f.tx.Exec(
		`INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id,
		                        story_id, provider, model)
		 VALUES ($1,$2,$3,$4,'anthropic','opus')`,
		"70000000-0000-7000-8000-0000000000c3", f.org, f.principal, f.story)
	if err == nil {
		t.Fatal("an LLM call named a Story with no Epic, escaping the lineage foreign key")
	}
	if !strings.Contains(err.Error(), "lineage_shape") {
		t.Errorf("failed on the wrong constraint: %v", err)
	}
}

// Provenance that can name the wrong parent is worse than none, because it
// reads as evidence.
func TestToolCallCannotClaimAnotherPrincipalsLLMCall(t *testing.T) {
	f := seed(t, openPlane(t))

	otherPrincipal := "70000000-0000-7000-8000-0000000000d1"
	if _, err := f.tx.Exec(
		`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		 VALUES ($1,$2,'agent','sonnet','coder')`, otherPrincipal, f.org); err != nil {
		t.Fatalf("seed principal: %v", err)
	}

	llmCall := "70000000-0000-7000-8000-0000000000d2"
	if _, err := f.tx.Exec(
		`INSERT INTO llm_calls (llm_call_id, organization_id, principal_instance_id, provider, model)
		 VALUES ($1,$2,$3,'anthropic','sonnet')`, llmCall, f.org, otherPrincipal); err != nil {
		t.Fatalf("seed llm call: %v", err)
	}

	// Same organization, different principal.
	err := f.insertToolCall("70000000-0000-7000-8000-0000000000d3", f.org, llmCall)
	if err == nil {
		t.Fatal("a tool call claimed an LLM call made by a different principal")
	}
}

// The provenance link exists to make attribution trustworthy, so a tool
// call and the LLM call it claims must agree on WHO is accountable, not
// merely on which principal ran and which Story it was for.
func TestToolCallCannotClaimAnLLMCallWithADifferentUser(t *testing.T) {
	var err error
	f := seed(t, openPlane(t))

	otherUser := "80000000-0000-7000-8000-000000000001"
	if _, err := f.tx.Exec(
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u2','U2')`,
		otherUser, f.org); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	llmCall := "80000000-0000-7000-8000-000000000002"
	if _, err := f.tx.Exec(
		`INSERT INTO llm_calls (llm_call_id, organization_id, user_id, principal_instance_id, provider, model)
		 VALUES ($1,$2,$3,$4,'anthropic','opus')`,
		llmCall, f.org, otherUser, f.principal); err != nil {
		t.Fatalf("seed llm call: %v", err)
	}

	// Same principal, same (empty) work tuple, different accountable user.
	//
	// Inside a savepoint: a rejected statement aborts its transaction, so
	// without one the positive case below would fail with "transaction is
	// aborted" while appearing to exercise its own constraint.
	f.rejects(t, "a tool call claimed an LLM call accountable to a different user",
		`INSERT INTO tool_calls (tool_call_id, organization_id, user_id, principal_instance_id, llm_call_id, tool_name, arguments)
		 VALUES ($1,$2,$3,$4,$5,'write_file','{}')`,
		"80000000-0000-7000-8000-000000000003", f.org, f.user, f.principal, llmCall)

	// The matching user must still be accepted, so the constraint is
	// discriminating rather than simply refusing every linked tool call.
	if _, err = f.tx.Exec(
		`INSERT INTO tool_calls (tool_call_id, organization_id, user_id, principal_instance_id, llm_call_id, tool_name, arguments)
		 VALUES ($1,$2,$3,$4,$5,'write_file','{}')`,
		"80000000-0000-7000-8000-000000000004", f.org, otherUser, f.principal, llmCall); err != nil {
		t.Errorf("a tool call with the SAME accountable user was rejected: %v", err)
	}
}
