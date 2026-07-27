// Package mergepatch implements RFC 7396 JSON Merge Patch.
//
// ADR 0028 encodes artifact amendments as merge patches and defines an
// artifact's effective view as its original payload with every accepted
// amendment applied in acceptance order. That assembly happens here, in Go,
// rather than in SQL: Postgres's `||` operator merges only the top level and
// cannot express deletion, so it silently computes something else.
//
// The distinction the whole format rests on is null-as-deletion. A null in
// the *patch* removes the key; a null already in the *target* is a value and
// survives. Any implementation that conflates the two — or that decodes into
// a structure unable to tell a present null from an absent key — produces
// plausible, wrong effective views.
package mergepatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
)

// apply returns the result of applying patch to target per RFC 7396.
//
// Neither argument is modified. The RFC's reference algorithm mutates the
// target in place, which is wrong here: targets are decoded artifact
// payloads that may be cached or shared, and applying an amendment to build
// a view must never alter the stored original it was built from.
//
// It is UNEXPORTED because its result is not a deep copy: subtrees the
// patch did not touch are shared with the target, so mutating the result
// could reach back into the stored original. Rather than ask every caller
// to honour that, the package exposes only ApplyJSON and ApplyChain, which
// return freshly encoded bytes and own nothing the caller can corrupt.
// Deep-copying here instead would cost on every effective-view read --
// which is almost entirely untouched subtrees -- to defend against a
// mutation no caller needs to make.
func apply(target, patch any) any {
	patchObject, patchIsObject := patch.(map[string]any)
	if !patchIsObject {
		// A non-object patch replaces the target wholesale, including when
		// the patch is null: at the top level null means "the document is
		// now null", not "delete the document".
		return patch
	}

	merged := map[string]any{}
	if targetObject, targetIsObject := target.(map[string]any); targetIsObject {
		maps.Copy(merged, targetObject)
	}
	// A non-object target is discarded rather than merged into, per the RFC.

	for key, patchValue := range patchObject {
		if patchValue == nil {
			delete(merged, key)
			continue
		}
		merged[key] = apply(merged[key], patchValue)
	}
	return merged
}

// ApplyJSON applies an encoded patch to an encoded target.
func ApplyJSON(target, patch []byte) ([]byte, error) {
	decodedTarget, err := decode(target)
	if err != nil {
		return nil, fmt.Errorf("decode target: %w", err)
	}
	decodedPatch, err := decode(patch)
	if err != nil {
		return nil, fmt.Errorf("decode patch: %w", err)
	}
	return encode(apply(decodedTarget, decodedPatch))
}

// ApplyChain builds an effective view: base with each patch applied in
// order. The order is the caller's responsibility and is significant —
// ADR 0028 fixes it as amendment acceptance order, and applying the same
// set in a different order can yield a different document.
func ApplyChain(base []byte, patches [][]byte) ([]byte, error) {
	current, err := decode(base)
	if err != nil {
		return nil, fmt.Errorf("decode base: %w", err)
	}
	for i, patch := range patches {
		decodedPatch, decodeErr := decode(patch)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode patch %d of %d: %w", i+1, len(patches), decodeErr)
		}
		current = apply(current, decodedPatch)
	}
	return encode(current)
}

// decode preserves numeric literals as json.Number so that a value written
// once is not silently reshaped by a decode/encode round trip on its way
// through an unrelated amendment. Canonicalization normalizes numbers
// later; this keeps the merge itself from being the thing that changed them.
func decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// More() is not an EOF check -- it reports whether another ELEMENT
	// follows within the value being decoded, so stray trailing tokens
	// like "]" or "}" slip past it. Decoding again and requiring io.EOF is
	// the actual check.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("trailing content after the JSON value: %w", err)
		}
		return nil, fmt.Errorf("trailing content after the JSON value: %s", trailing)
	}
	return value, nil
}

func encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("encode merged document: %w", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
