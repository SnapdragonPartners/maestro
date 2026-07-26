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
