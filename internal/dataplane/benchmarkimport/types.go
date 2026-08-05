package benchmarkimport

import (
	"encoding/json"
	"fmt"

	"orchestrator/internal/dataplane/registry"
)

// The artifact types this item registers.
//
// ADR 0028's registry ships no seed vocabulary: a type is registered by the
// item that first writes it, because a registered type with no consumer is a
// guess about a future caller. These are the first two.
const (
	// TypeRunRecord is one attempt's normalized result. AUDIT: a measurement
	// is exhaust, born final, with no lifecycle to move through. It is
	// truncatable unless pinned, which is what the suite report's pins are
	// for — a conformance claim whose underlying records can be pruned is a
	// claim that decays.
	TypeRunRecord registry.Type = "benchmark.run_record"

	// TypeSuiteReport is the operator's claim about one suite run.
	// MANAGEMENT: it is reviewable work product, and it is the only family
	// that may hold a retention pin.
	TypeSuiteReport registry.Type = "benchmark.suite_report"
)

// PayloadVersion is the schema version both payloads are written at.
const PayloadVersion = 1

// RunRecordPayload is the body of a benchmark.run_record artifact.
//
// The record is carried whole rather than flattened into columns. ADR 0028
// makes the envelope the schema and the payload the body, and the record is
// already a normalized contract with its own version — re-modelling it here
// would be a second schema to keep in step with the first.
type RunRecordPayload struct {
	// ImportedFrom names the store the record was read from, so a reader can
	// tell where the bytes came from without consulting the ledger.
	ImportedFrom string `json:"imported_from"`
	Record       Record `json:"record"`
}

// RegistryEntries returns the registrations this package writes.
//
// Returned rather than registered globally: the registry is immutable by
// construction and built once at startup, so a package that mutated a shared
// one would be exactly the run-time mutation ADR 0028 forbids.
func RegistryEntries() map[registry.Type]registry.Entry {
	return map[registry.Type]registry.Entry{
		TypeRunRecord: {
			Category:       registry.CategoryAudit,
			CurrentVersion: PayloadVersion,
			Validators: map[int]registry.Validator{
				PayloadVersion: registry.ValidatorFunc(validateRunRecordPayload),
			},
			// No extractor, deliberately. Its absence is a STATEMENT: a run
			// record carries no evidence of its own, so acceptance requires
			// exactly zero pins for it. The suite report is what holds them.
		},
	}
}

// validateRunRecordPayload checks a payload is a well-formed run record.
//
// The same rules the reader applies, run again at the seam. Not redundant:
// the reader validates what came off disk, and this validates what a caller
// hands the plane — which need not be the same bytes, and on a read of an
// older artifact certainly is not.
func validateRunRecordPayload(payload []byte) error {
	var body RunRecordPayload
	decoder := json.NewDecoder(newStrictReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return fmt.Errorf("benchmark.run_record payload: %w", err)
	}
	if body.ImportedFrom == "" {
		return fmt.Errorf("benchmark.run_record payload: imported_from is required")
	}
	if err := body.Record.Validate(); err != nil {
		return fmt.Errorf("benchmark.run_record payload: %w", err)
	}
	return nil
}
