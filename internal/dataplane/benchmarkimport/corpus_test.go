package benchmarkimport_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/benchmarkimport"
)

// corpusDir is the two-sided conformance corpus. It lives under benchmark/
// because BOTH modules read it: this package's validator and the runner's own
// must agree on every case.
const corpusDir = "../../../benchmark/testdata/import_corpus"

// corpusCase is one case as it appears on disk.
type corpusCase struct {
	// Record is the record under test. Absent when RawLine is used, because
	// some cases are about bytes a valid record cannot express.
	Record json.RawMessage `json:"record"`
	// RawLine overrides Record for cases about the LINE rather than the
	// record — trailing content, for instance, which cannot be represented
	// as a JSON object.
	RawLine string `json:"raw_line,omitempty"`

	Expect string `json:"expect"`
	Reason string `json:"reason,omitempty"`

	// RunnerExpect is set ONLY where the two validators legitimately differ,
	// and Divergence must say why. Declaring it is what makes an UNDECLARED
	// disagreement a failure: silence means the sides must agree.
	RunnerExpect string `json:"runner_expect,omitempty"`
	Divergence   string `json:"divergence,omitempty"`
}

func loadCorpus(t *testing.T) map[string]corpusCase {
	t.Helper()
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	cases := make(map[string]corpusCase, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var testCase corpusCase
		if err := json.Unmarshal(raw, &testCase); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		switch testCase.Expect {
		case "accept", "reject":
		default:
			t.Fatalf("%s: expect is %q, want accept or reject", entry.Name(), testCase.Expect)
		}
		if testCase.Expect == "reject" && testCase.Reason == "" {
			t.Fatalf("%s: a reject case must name the rule it exercises", entry.Name())
		}
		if testCase.RunnerExpect != "" && testCase.Divergence == "" {
			t.Fatalf("%s: a declared divergence must say WHY the two sides differ", entry.Name())
		}
		cases[strings.TrimSuffix(entry.Name(), ".json")] = testCase
	}
	if len(cases) == 0 {
		t.Fatal("the corpus is empty; every assertion below would pass vacuously")
	}
	return cases
}

// line returns the JSONL line a case should be fed.
func (c corpusCase) line(t *testing.T) string {
	t.Helper()
	if c.RawLine != "" {
		return c.RawLine
	}
	return string(c.Record)
}

// TestCorpusAgreesWithTheImporter runs every case through this package's
// validator and asserts the declared outcome.
func TestCorpusAgreesWithTheImporter(t *testing.T) {
	for name, testCase := range loadCorpus(t) {
		t.Run(name, func(t *testing.T) {
			record, err := benchmarkimport.DecodeRecord(testCase.line(t))
			if err == nil {
				err = record.Validate()
			}
			if testCase.Expect == "accept" {
				if err != nil {
					t.Fatalf("must be accepted, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("must be refused by %q, got no error", testCase.Reason)
			}
			var rejection *benchmarkimport.Rejection
			if !asRejection(err, &rejection) {
				t.Fatalf("must be refused by a named rule, got: %v", err)
			}
			// The NAMED rule, not merely some rule. Most of these records
			// could break more than one, and a test that only asks whether an
			// error happened cannot tell which one spoke.
			if string(rejection.Reason) != testCase.Reason {
				t.Fatalf("refused by %q, want %q", rejection.Reason, testCase.Reason)
			}
		})
	}
}

// TestEveryRejectionReasonHasACase derives the reason list from the SOURCE.
//
// A hand-written coverage list is the enumeration this repository has had to
// fix three times after it silently fell behind. Walking the const block
// cannot fall behind: a reason added without a corpus case fails here.
func TestEveryRejectionReasonHasACase(t *testing.T) {
	exercised := make(map[string]bool)
	for _, testCase := range loadCorpus(t) {
		if testCase.Expect == "reject" {
			exercised[testCase.Reason] = true
		}
	}

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "reasons.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse reasons.go: %v", err)
	}
	found := 0
	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, isValue := node.(*ast.ValueSpec)
		if !isValue {
			return true
		}
		for _, value := range spec.Values {
			literal, isLiteral := value.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				continue
			}
			text, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				continue
			}
			found++
			if !exercised[text] {
				t.Errorf("rejection reason %q has no corpus case; every rule this package "+
					"can refuse for must be exercised on both sides", text)
			}
		}
		return true
	})
	if found == 0 {
		t.Fatal("no rejection reasons were discovered; the AST walk is not finding the const block, " +
			"so this test would pass however many reasons went uncovered")
	}
}

// asRejection is errors.As without importing errors into every call site.
func asRejection(err error, target **benchmarkimport.Rejection) bool {
	for err != nil {
		if rejection, ok := err.(*benchmarkimport.Rejection); ok { //nolint:errorlint // deliberate single-step
			*target = rejection
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error }) //nolint:errorlint // deliberate single-step
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
