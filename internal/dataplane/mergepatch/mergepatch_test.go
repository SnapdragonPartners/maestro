package mergepatch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/canonical"
)

// appendixA is the complete example table from RFC 7396 Appendix A. It is
// reproduced in full rather than sampled: the cases most likely to be
// implemented wrongly are the odd ones at the bottom — a null already in the
// target, a non-object target, a deletion of a key that was never there —
// and those are exactly the ones a curated subset tends to drop.
var appendixA = []struct {
	original string
	patch    string
	result   string
}{
	{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
	{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
	{`{"a":"b"}`, `{"a":null}`, `{}`},
	{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
	{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
	{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
	{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
	{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
	{`["a","b"]`, `["c","d"]`, `["c","d"]`},
	{`{"a":"b"}`, `["c"]`, `["c"]`},
	{`{"a":"foo"}`, `null`, `null`},
	{`{"a":"foo"}`, `"bar"`, `"bar"`},
	{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
	{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
	{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
}

func TestRFC7396AppendixA(t *testing.T) {
	for _, vector := range appendixA {
		t.Run(vector.original+" + "+vector.patch, func(t *testing.T) {
			got, err := ApplyJSON([]byte(vector.original), []byte(vector.patch))
			if err != nil {
				t.Fatalf("ApplyJSON: %v", err)
			}
			assertSameDocument(t, got, []byte(vector.result))
		})
	}
}

// TestRFC7396Section3Example is the RFC's worked example, which exercises
// nested replacement, deletion and array replacement together rather than
// one behaviour at a time.
func TestRFC7396Section3Example(t *testing.T) {
	original := `{
     "title": "Goodbye!",
     "author": {"givenName": "John", "familyName": "Doe"},
     "tags": ["example", "sample"],
     "content": "This will be unchanged"
   }`
	patch := `{
     "title": "Hello!",
     "phoneNumber": "+01-123-456-7890",
     "author": {"familyName": null},
     "tags": ["example"]
   }`
	want := `{
     "title": "Hello!",
     "author": {"givenName": "John"},
     "tags": ["example"],
     "content": "This will be unchanged",
     "phoneNumber": "+01-123-456-7890"
   }`

	got, err := ApplyJSON([]byte(original), []byte(patch))
	if err != nil {
		t.Fatalf("ApplyJSON: %v", err)
	}
	assertSameDocument(t, got, []byte(want))
}

// TestApplyDoesNotMutateTarget guards the deviation from the RFC's reference
// algorithm, which mutates the target in place. Here targets are decoded
// artifact payloads: building an effective view must not alter the stored
// original it was assembled from, or the second reader of a cached payload
// sees the first reader's amendments.
//
// This tests the unexported apply directly. The exported surface returns
// encoded bytes precisely so this hazard cannot reach a caller.
func TestApplyDoesNotMutateTarget(t *testing.T) {
	target := map[string]any{
		"keep":   "value",
		"drop":   "value",
		"nested": map[string]any{"inner": "value"},
	}
	before := deepCopy(t, target)

	apply(target, map[string]any{
		"drop":   nil,
		"nested": map[string]any{"inner": "changed"},
		"added":  "new",
	})

	if !reflect.DeepEqual(target, before) {
		t.Fatalf("apply mutated its target\n got: %v\nwant: %v", target, before)
	}
}

// TestApplyChainAppliesInOrder pins the ordering contract. Merge patch is
// not commutative, so an effective view assembled in the wrong order is
// wrong rather than merely differently ordered.
func TestApplyChainAppliesInOrder(t *testing.T) {
	base := []byte(`{"status":"draft"}`)
	first := []byte(`{"status":"review"}`)
	second := []byte(`{"status":"accepted"}`)

	forward, err := ApplyChain(base, [][]byte{first, second})
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	assertSameDocument(t, forward, []byte(`{"status":"accepted"}`))

	reverse, err := ApplyChain(base, [][]byte{second, first})
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	assertSameDocument(t, reverse, []byte(`{"status":"review"}`))
}

// TestApplyChainReAddsAfterDeletion covers the case where a later amendment
// restores a key an earlier one removed — the sequence that a set-union
// implementation gets wrong in both directions.
func TestApplyChainReAddsAfterDeletion(t *testing.T) {
	got, err := ApplyChain([]byte(`{"a":"one","b":"two"}`), [][]byte{
		[]byte(`{"a":null}`),
		[]byte(`{"a":"three"}`),
	})
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	assertSameDocument(t, got, []byte(`{"a":"three","b":"two"}`))
}

func TestApplyChainWithNoPatchesReturnsBase(t *testing.T) {
	got, err := ApplyChain([]byte(`{"a":"b"}`), nil)
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	assertSameDocument(t, got, []byte(`{"a":"b"}`))
}

func TestApplyChainReportsWhichPatchFailed(t *testing.T) {
	_, err := ApplyChain([]byte(`{"a":"b"}`), [][]byte{
		[]byte(`{"a":"c"}`),
		[]byte(`{"a":`),
	})
	if err == nil {
		t.Fatal("expected an error for a malformed patch")
	}
	if want := "patch 2 of 2"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not identify the failing patch (want %q)", err, want)
	}
}

// TestDecodeRejectsTrailingContent covers the cases a Decoder.More() check
// waves through. More() reports whether another element follows inside the
// value being decoded, not whether input is exhausted, so a stray closing
// bracket after a complete document reads as "no more elements" and the
// malformed payload is accepted -- and then digested and stored.
func TestDecodeRejectsTrailingContent(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"two concatenated documents", `{"a":"b"} {"c":"d"}`},
		{"trailing close bracket", `{"a":"b"}]`},
		{"trailing close brace", `{"a":"b"}}`},
		{"trailing comma", `{"a":"b"},`},
		{"trailing garbage", `{"a":"b"} nonsense`},
		{"array with trailing brace", `["a"]}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ApplyJSON([]byte(testCase.input), []byte(`{}`)); err == nil {
				t.Fatalf("accepted malformed input %s", testCase.input)
			}
		})
	}
}

// assertSameDocument compares by canonical form, so the comparison is about
// the documents rather than about key order or whitespace.
func assertSameDocument(t *testing.T, got, want []byte) {
	t.Helper()

	gotDigest, err := canonical.DigestJSON(got)
	if err != nil {
		t.Fatalf("canonicalize result %s: %v", got, err)
	}
	wantDigest, err := canonical.DigestJSON(want)
	if err != nil {
		t.Fatalf("canonicalize expected %s: %v", want, err)
	}
	if gotDigest != wantDigest {
		t.Fatalf("document mismatch\n got: %s\nwant: %s", got, want)
	}
}

func deepCopy(t *testing.T, value any) any {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal for deep copy: %v", err)
	}
	var copied any
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatalf("unmarshal for deep copy: %v", err)
	}
	return copied
}
