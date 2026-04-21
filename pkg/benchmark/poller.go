package benchmark

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // SQLite driver.
)

// Outcome classifies the terminal state of a benchmark instance.
type Outcome string

// Benchmark outcome constants.
const (
	OutcomeSuccess         Outcome = "success"
	OutcomeTerminalFailure Outcome = "terminal_failure"
	OutcomeStalled         Outcome = "stalled"
	OutcomeTimeout         Outcome = "timeout"
	OutcomeProcessError    Outcome = "process_error"
)

// PollConfig controls polling behavior.
type PollConfig struct {
	DBPath     string        // Path to maestro.db
	SpecID     string        // Spec ID to monitor
	SessionID  string        // Session ID for filtering
	Interval   time.Duration // Poll interval (default 10s)
	Timeout    time.Duration // Total timeout (default 60m)
	StallGrace time.Duration // Grace period for on_hold stories (default 5m)
}

func (c *PollConfig) defaults() {
	if c.Interval == 0 {
		c.Interval = 10 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 60 * time.Minute
	}
	if c.StallGrace == 0 {
		c.StallGrace = 5 * time.Minute
	}
}

// storyRow is the minimal projection we read from the stories table.
type storyRow struct {
	HoldSince *time.Time
	ID        string
	Status    string
}

// PollForCompletion blocks until the benchmark instance reaches a terminal state.
// Returns the outcome and any error in establishing DB connection (not Maestro errors).
func PollForCompletion(ctx context.Context, cfg PollConfig) (Outcome, error) {
	cfg.defaults()

	db, err := openReadOnly(cfg.DBPath)
	if err != nil {
		return OutcomeProcessError, fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	deadline := time.Now().Add(cfg.Timeout)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return OutcomeTimeout, fmt.Errorf("context cancelled: %w", ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return OutcomeTimeout, nil
			}

			outcome, classifyErr := classifyOnce(db, cfg)
			if classifyErr != nil {
				// Transient DB errors — retry on next tick.
				continue
			}
			if outcome != "" {
				return outcome, nil
			}
			// Empty outcome means still in progress.
		}
	}
}

// classifyOnce reads current story states and returns the outcome.
// Empty string means "still in progress".
func classifyOnce(db *sql.DB, cfg PollConfig) (Outcome, error) {
	rows, err := queryStories(db, cfg.SpecID, cfg.SessionID)
	if err != nil {
		return "", err
	}

	if len(rows) == 0 {
		return "", nil
	}

	counts := countStoryStates(rows, cfg.StallGrace)
	return classifyFromCounts(counts, len(rows)), nil
}

type storyCounts struct {
	done, failed, hold, active int
	allHoldStale               bool
}

func countStoryStates(rows []storyRow, stallGrace time.Duration) storyCounts {
	c := storyCounts{allHoldStale: true}
	now := time.Now()
	for i := range rows {
		switch rows[i].Status {
		case "done":
			c.done++
		case "failed":
			c.failed++
		case "on_hold":
			c.hold++
			if rows[i].HoldSince == nil || now.Sub(*rows[i].HoldSince) < stallGrace {
				c.allHoldStale = false
			}
		default: // new, pending, dispatched, planning, coding
			c.active++
		}
	}
	return c
}

func classifyFromCounts(c storyCounts, total int) Outcome {
	if c.done == total {
		return OutcomeSuccess
	}
	if c.active == 0 && c.hold == 0 && c.failed > 0 {
		return OutcomeTerminalFailure
	}
	if c.active == 0 && c.hold > 0 && c.allHoldStale {
		return OutcomeStalled
	}
	return ""
}

func queryStories(db *sql.DB, specID, sessionID string) ([]storyRow, error) {
	query := `SELECT id, status, hold_since FROM stories WHERE spec_id = ? AND session_id = ?`
	rows, err := db.Query(query, specID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query stories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []storyRow
	for rows.Next() {
		var r storyRow
		var holdSince sql.NullTime
		if scanErr := rows.Scan(&r.ID, &r.Status, &holdSince); scanErr != nil {
			return nil, fmt.Errorf("scan story row: %w", scanErr)
		}
		if holdSince.Valid {
			r.HoldSince = &holdSince.Time
		}
		result = append(result, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate story rows: %w", rowsErr)
	}
	return result, nil
}

func openReadOnly(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL&_busy_timeout=5000", url.PathEscape(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	if pingErr := db.Ping(); pingErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db ping: %w", pingErr)
	}
	return db, nil
}
