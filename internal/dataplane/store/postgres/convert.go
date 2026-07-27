package postgres

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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

// toNullTimestamptz converts an optional domain time.
func toNullTimestamptz(at *time.Time) pgtype.Timestamptz {
	if at == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *at, Valid: true}
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
