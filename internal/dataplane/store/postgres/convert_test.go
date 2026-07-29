package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/store"
)

// The D9 money boundary, tested in both directions.
//
// These are unit tests rather than integration ones because the cases that
// matter most cannot be produced through SQL at all: the aggregate's total
// is COALESCEd, so a null can only arrive from the query or this
// conversion having been changed, and the integration suite would prove
// only that today's query happens to be right.

func TestCostRoundTripsExactly(t *testing.T) {
	// Eight fractional digits is the column's scale and binary64's limit is
	// well short of it: 0.00000001 has no exact float64 representation, so
	// a conversion that went through one would not return this text.
	for _, text := range []string{"0.00000000", "0.00000001", "1.23456789", "9999999999.99999999"} {
		cost := store.MustParseUSD(text)
		encoded, err := toNumeric(&cost)
		if err != nil {
			t.Fatalf("to numeric %s: %v", text, err)
		}
		decoded, err := fromNumeric(encoded)
		if err != nil {
			t.Fatalf("from numeric %s: %v", text, err)
		}
		if decoded == nil {
			t.Fatalf("cost %s round-tripped to nil", text)
		}
		if decoded.String() != text {
			t.Errorf("cost round-tripped to %s, want %s", decoded, text)
		}
	}
}

// TestAbsentCostStaysAbsent is the load-bearing null. On a completed call it
// means the cost is not KNOWABLE -- paired-local's local models -- which is
// a different fact from zero, a real measurement.
func TestAbsentCostStaysAbsent(t *testing.T) {
	encoded, err := toNumeric(nil)
	if err != nil {
		t.Fatalf("to numeric nil: %v", err)
	}
	if encoded.Valid {
		t.Error("a nil cost encoded as a valid numeric, which Postgres stores as a value rather than NULL")
	}
	decoded, err := fromNumeric(pgtype.Numeric{})
	if err != nil {
		t.Fatalf("from numeric null: %v", err)
	}
	if decoded != nil {
		t.Errorf("a null cost decoded as %v, want nil; zero and unknown are different facts", decoded)
	}
}

// TestNullAggregateTotalIsAnInvariantFailure covers what the database
// cannot produce. AggregateLLMCost COALESCEs its SUM, so a null total means
// the query or this conversion is broken -- and reading it as zero would
// make that indistinguishable from a cohort that genuinely cost nothing.
func TestNullAggregateTotalIsAnInvariantFailure(t *testing.T) {
	total, err := fromNumericTotal(pgtype.Numeric{})
	if err == nil {
		t.Fatalf("a null aggregate total converted to %s with no error", total)
	}
	if !errors.Is(err, store.ErrInvariant) {
		t.Errorf("error %v is not an ErrInvariant, so a caller cannot tell it from a bad input", err)
	}
}

// TestAggregateTotalExceedsARowsRange is why USDTotal is a distinct type: a
// SUM is not bounded by numeric(18,8)'s ten integer digits, and applying
// the row bound to a campaign total would reject a correct number for being
// large.
func TestAggregateTotalExceedsARowsRange(t *testing.T) {
	const huge = "12345678901234.00000000" // fourteen integer digits
	if _, err := store.ParseUSD(huge); err == nil {
		t.Fatal("a row cost accepted fourteen integer digits, so this test proves nothing about the total")
	}

	var value pgtype.Numeric
	if err := value.Scan(huge); err != nil {
		t.Fatalf("scan: %v", err)
	}
	total, err := fromNumericTotal(value)
	if err != nil {
		t.Fatalf("from numeric total: %v", err)
	}
	if total.String() != huge {
		t.Errorf("total converted to %s, want %s", total, huge)
	}
}

// TestOptionalInstantTreatsZeroAsAbsent guards the born-final event types,
// whose timestamps are values rather than pointers. A zero time.Time is
// year 1: stored literally it would place every such row past any retention
// horizon the instant it was written.
func TestOptionalInstantTreatsZeroAsAbsent(t *testing.T) {
	if optionalInstant(time.Time{}).Valid {
		t.Error("the zero time encoded as a value; SQL's now() would never fill the column")
	}
	at := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	encoded := optionalInstant(at)
	if !encoded.Valid || !encoded.Time.Equal(at) {
		t.Errorf("instant encoded as %+v, want %s", encoded, at)
	}
}

// TestRetryStopsAtTheBound exercises the contract the live concurrency test
// cannot: three attempts is a bound, and exhausting it must produce the
// seam's typed error rather than a raw driver failure every caller would
// have to decode.
func TestRetryStopsAtTheBound(t *testing.T) {
	attempts := 0
	_, err := retryOnSerializationFailure(truncationAttempts, func() (int, error) {
		attempts++
		return 0, &pgconn.PgError{Code: serializationFailure, Message: "could not serialize access"}
	})
	if attempts != truncationAttempts {
		t.Errorf("ran %d attempts, want %d", attempts, truncationAttempts)
	}
	if !errors.Is(err, store.ErrConcurrentTruncation) {
		t.Errorf("error %v is not an ErrConcurrentTruncation", err)
	}
}

// TestRetrySucceedsAfterAConflict is the other half: a pass that loses one
// race and wins the next must return its result, not the error it survived.
func TestRetrySucceedsAfterAConflict(t *testing.T) {
	attempts := 0
	result, err := retryOnSerializationFailure(truncationAttempts, func() (string, error) {
		attempts++
		if attempts == 1 {
			return "", &pgconn.PgError{Code: serializationFailure}
		}
		return "committed", nil
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result != "committed" || attempts != 2 {
		t.Errorf("got %q after %d attempts, want %q after 2", result, attempts, "committed")
	}
}

// TestRetryDoesNotRetryOtherErrors keeps the loop from turning an ordinary
// failure into three identical ones followed by a misleading concurrency
// error. A constraint violation is not a lost race.
func TestRetryDoesNotRetryOtherErrors(t *testing.T) {
	attempts := 0
	violation := &pgconn.PgError{Code: "23503", ConstraintName: provenanceConstraint}
	_, err := retryOnSerializationFailure(truncationAttempts, func() (int, error) {
		attempts++
		return 0, violation
	})
	if attempts != 1 {
		t.Errorf("ran %d attempts on a non-serialization error, want 1", attempts)
	}
	if !errors.Is(err, violation) {
		t.Errorf("error %v does not carry the original failure", err)
	}
	if errors.Is(err, store.ErrConcurrentTruncation) {
		t.Error("a foreign key violation was reported as a truncation concurrency failure")
	}
}

// TestConstraintMatchingIsByName covers the provenance translation's
// premise: the seam matches the constraint NAME, because message text is
// reworded between Postgres versions.
func TestConstraintMatchingIsByName(t *testing.T) {
	matching := &pgconn.PgError{Code: "23503", ConstraintName: provenanceConstraint}
	if !violatesProvenanceKey(matching) {
		t.Error("the provenance constraint violation was not recognised")
	}
	other := &pgconn.PgError{Code: "23503", ConstraintName: "tool_calls_principal_fkey"}
	if violatesProvenanceKey(other) {
		t.Error("a different foreign key was read as a provenance violation, which would blame the wrong claim")
	}
	if violatesProvenanceKey(errors.New("connection reset")) {
		t.Error("a non-Postgres error matched a constraint name")
	}
}
