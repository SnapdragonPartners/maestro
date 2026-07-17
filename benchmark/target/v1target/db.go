package v1target

// SQLite observation of v1's maestro.db.
//
// DEPENDENCY NOTE: modernc.org/sqlite is an adapter-scoped v1-compatibility
// dependency; its import is confined to this package. Removal trigger:
// retiring the v1 adapter or replacing its observation surface (see
// design_adapter_v1.md).

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (v1-compat, this package only)
)

// storyState is the minimal projection of one v1 story row.
type storyState struct {
	ID     string
	Status string
	PRID   string
	Tokens int64
	Cost   float64
}

// v1DB wraps a read-only connection to a maestro.db.
type v1DB struct {
	db *sql.DB
}

func openV1DB(path string) (*v1DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open maestro.db: %w", err)
	}
	return &v1DB{db: db}, nil
}

func (v *v1DB) close() error {
	if err := v.db.Close(); err != nil {
		return fmt.Errorf("close maestro.db: %w", err)
	}
	return nil
}

// discover returns the first spec and session IDs once they exist.
func (v *v1DB) discover(ctx context.Context) (specID, sessionID string, err error) {
	row := v.db.QueryRowContext(ctx,
		`SELECT id, session_id FROM specs ORDER BY created_at LIMIT 1`)
	if scanErr := row.Scan(&specID, &sessionID); scanErr != nil {
		return "", "", fmt.Errorf("discover spec: %w", scanErr)
	}
	return specID, sessionID, nil
}

// stories returns the current state of every story for the spec/session.
func (v *v1DB) stories(ctx context.Context, specID, sessionID string) ([]storyState, error) {
	rows, err := v.db.QueryContext(ctx,
		`SELECT id, status, COALESCE(pr_id, ''), COALESCE(tokens_used, 0), COALESCE(cost_usd, 0)
		 FROM stories WHERE spec_id = ? AND session_id = ?`, specID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query stories: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows
	var out []storyState
	for rows.Next() {
		var s storyState
		if err := rows.Scan(&s.ID, &s.Status, &s.PRID, &s.Tokens, &s.Cost); err != nil {
			return nil, fmt.Errorf("scan story: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stories: %w", err)
	}
	return out, nil
}

// toolCallCount counts tool executions for the session.
func (v *v1DB) toolCallCount(ctx context.Context, sessionID string) (int64, error) {
	var count int64
	row := v.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tool_executions WHERE session_id = ?`, sessionID)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count tool executions: %w", err)
	}
	return count, nil
}

// classification of the story set (all-stories terminal semantics: every
// story done is success; any failed/skipped/on_hold terminal row is not).
type classification struct {
	states     []storyState
	total      int
	done       int
	failedTerm int
}

func classify(states []storyState) classification {
	c := classification{states: states, total: len(states)}
	for i := range states {
		switch states[i].Status {
		case "done":
			c.done++
		case "failed", "skipped", "on_hold":
			c.failedTerm++
		}
	}
	return c
}

func (c classification) allDone() bool {
	return c.total > 0 && c.done == c.total
}

func (c classification) terminal() bool {
	return c.total > 0 && (c.done+c.failedTerm) == c.total
}

func (c classification) aggregates() (tokens int64, cost float64) {
	for i := range c.states {
		tokens += c.states[i].Tokens
		cost += c.states[i].Cost
	}
	return tokens, cost
}

// snapshotDB produces a consistent copy of a WAL-mode maestro.db via SQLite
// online backup (VACUUM INTO): a raw file copy after a kill can miss the
// newest WAL frames; opening the database replays them.
func snapshotDB(ctx context.Context, srcPath, destPath string) error {
	db, err := sql.Open("sqlite", "file:"+srcPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open for snapshot: %w", err)
	}
	defer db.Close() //nolint:errcheck // snapshot connection
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destPath); err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}
