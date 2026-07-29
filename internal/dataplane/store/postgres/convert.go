package postgres

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/store"
)

// This file is design D9's boundary: pgtype goes no further up than here.
//
// The conversions are explicit rather than incidental because pgx generates
// uuid and timestamptz as pgtype.UUID and pgtype.Timestamptz whether the
// column is nullable or not, each carrying its own Valid flag. A Valid flag
// dropped on the floor turns an absent reviewer into the zero UUID — a
// value that looks like data, reads like data, and joins like data.
//
// Every function here has a null case, and every null case is tested in
// both directions.

// toUUID converts a domain UUID to the driver's.
func toUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// toNullUUID converts an optional domain UUID. A nil pointer becomes an
// invalid pgtype.UUID, which Postgres writes as NULL — not as the zero UUID.
func toNullUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// fromUUID converts a driver UUID that the schema guarantees is NOT NULL.
// An invalid value here means the column was nullable after all, so it
// yields the zero UUID rather than panicking — the caller's own row
// validation is the place that failure should surface.
func fromUUID(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return id.Bytes
}

// fromNullUUID converts a nullable driver UUID, preserving absence as nil.
func fromNullUUID(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes)
	return &value
}

// toTimestamptz converts a domain time the schema guarantees is present.
//
// Removed once as unused and restored when the aggregate's mandatory window
// gave it a consumer -- the window bounds are required, so they are never
// the nullable form.
func toTimestamptz(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: true}
}

// toNullTimestamptz converts an optional domain time.
func toNullTimestamptz(at *time.Time) pgtype.Timestamptz {
	if at == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *at, Valid: true}
}

// optionalInstant treats the ZERO time as absent, so SQL's now() fills the
// column.
//
// Distinct from toNullTimestamptz, which takes a pointer: the born-final
// event types carry their timestamp by value, and a zero time.Time is year
// 1 -- not a plausible instant for a measurement, and one that would place
// every such row past any retention horizon the moment it was written.
func optionalInstant(at time.Time) pgtype.Timestamptz {
	if at.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: at, Valid: true}
}

// fromTimestamptz converts a driver timestamp the schema guarantees is NOT
// NULL. An invalid value yields the zero time.
func fromTimestamptz(at pgtype.Timestamptz) time.Time {
	if !at.Valid {
		return time.Time{}
	}
	return at.Time
}

// fromNullTimestamptz converts a nullable driver timestamp, preserving
// absence as nil.
func fromNullTimestamptz(at pgtype.Timestamptz) *time.Time {
	if !at.Valid {
		return nil
	}
	value := at.Time
	return &value
}

// toNullInt32 narrows an optional int for a nullable int4 column, failing
// rather than wrapping.
//
// An unchecked int32() conversion is silent and dangerous here: on a 64-bit
// build 4294967297 narrows to 1, and a base sequence of 1 is a REAL
// sequence. A caller passing a nonsense value would not get an error — it
// would get a review bound to a base the reviewer never named.
func toNullInt32(value *int) (*int32, error) {
	if value == nil {
		return nil, nil //nolint:nilnil // absent is not an error here
	}
	if *value < 0 || *value > math.MaxInt32 {
		return nil, fmt.Errorf("value %d is outside the nonnegative int32 range this column stores", *value)
	}
	narrowed := int32(*value)
	return &narrowed, nil
}

// toInt32 narrows a non-nullable int for an int4 column, failing rather
// than wrapping.
//
// Schema versions reach this from the registry, which is configuration: an
// unchecked int32() turns 4294967297 into 1, so an artifact would validate
// under one schema version and record another. The narrowing is silent, and
// the resulting row looks entirely ordinary.
func toInt32(value int, what string) (int32, error) {
	if value < 0 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s %d is outside the nonnegative int32 range this column stores", what, value)
	}
	return int32(value), nil
}

// fromNullInt32 widens a nullable int column into the domain type.
func fromNullInt32(value *int32) *int {
	if value == nil {
		return nil
	}
	widened := int(*value)
	return &widened
}

// fromNullString copies an optional string so the returned pointer does not
// alias the row struct the driver may reuse.
func fromNullString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// --- money -----------------------------------------------------------------
//
// The D9 boundary for cost. numeric crosses the driver as pgtype.Numeric,
// which carries an arbitrary-precision integer and an exponent; the domain
// types are store.USD for a row value and store.USDTotal for an aggregate,
// which have different ranges and so cannot share one conversion.
//
// Both directions go through the DECIMAL TEXT, never through float64.
// pgtype.Numeric offers a Float64Value method and using it here would
// silently undo the entire reason cost is stored exactly.

// toNumeric converts a row cost for writing.
func toNumeric(cost *store.USD) (pgtype.Numeric, error) {
	if cost == nil {
		return pgtype.Numeric{}, nil
	}
	var value pgtype.Numeric
	if err := value.Scan(cost.String()); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("convert cost %s for storage: %w", cost, err)
	}
	return value, nil
}

// fromNumeric converts a nullable row cost, preserving absence as nil.
//
// Absence is load-bearing: on an open call it means the cost is not known
// YET, and on a completed one that it is not knowable at all. Neither is
// zero, which is a real measurement.
func fromNumeric(value pgtype.Numeric) (*store.USD, error) {
	text, ok, err := numericText(value)
	if err != nil || !ok {
		return nil, err
	}
	cost, err := store.ParseUSD(text)
	if err != nil {
		return nil, fmt.Errorf("read stored cost %q: %w", text, err)
	}
	return &cost, nil
}

// fromNumericTotal converts an aggregate. A SUM is not bounded by the row
// column's typmod, so it parses under the aggregate's wider range.
func fromNumericTotal(value pgtype.Numeric) (store.USDTotal, error) {
	text, ok, err := numericText(value)
	if err != nil {
		return store.USDTotal{}, err
	}
	if !ok {
		// The aggregate query COALESCEs its SUM, so a null total cannot
		// arise from data -- only from the query or this conversion having
		// been changed. Reading it as zero would hide that behind a value
		// indistinguishable from a real zero-cost cohort, which is the one
		// number the benchmark's economic comparison turns on.
		return store.USDTotal{}, fmt.Errorf(
			"%w: aggregate cost total came back null, but the query guarantees a non-null total",
			store.ErrInvariant)
	}
	total, err := store.ParseUSDTotal(text)
	if err != nil {
		return store.USDTotal{}, fmt.Errorf("read aggregate cost %q: %w", text, err)
	}
	return total, nil
}

// numericText renders a pgtype.Numeric as decimal text.
func numericText(value pgtype.Numeric) (text string, present bool, err error) {
	if !value.Valid {
		return "", false, nil
	}
	rendered, err := value.Value()
	if err != nil {
		return "", false, fmt.Errorf("render numeric: %w", err)
	}
	asString, ok := rendered.(string)
	if !ok {
		return "", false, fmt.Errorf("numeric rendered as %T, want a decimal string", rendered)
	}
	return asString, true, nil
}
