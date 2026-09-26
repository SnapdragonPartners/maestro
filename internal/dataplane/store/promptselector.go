package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
)

// PromptPackKey is the configuration key that selects an organization's,
// product's or repository's prompt pack (ADR 0031 section 4; Phase 3 item 4
// design, D7). The Orchestrator registers it; dispatch resolves it. The
// vocabulary lives here rather than in either of them because both must
// agree on what a selector IS, and the seam may not import the Orchestrator.
const PromptPackKey configkeys.Key = "prompt.pack"

// ErrPromptSelectorIsAName reports a selector that tried to name a pack by
// its label. A name is a non-unique label and every edit is a new version,
// so a name cannot deterministically identify one (ADR 0031 section 1); the
// error says so, so the failure teaches the rule.
var ErrPromptSelectorIsAName = errors.New("a prompt pack selector cannot be a name: a name is a label, not an identity; " +
	"select by content record id or by scheme-qualified digest")

// PromptSelector names exactly one pack version, by content record or by
// rendered identity. Exactly one field is set.
type PromptSelector struct {
	ContentID *uuid.UUID
	Identity  *PromptIdentity
}

// String renders the selector for diagnostics.
func (s PromptSelector) String() string {
	switch {
	case s.ContentID != nil:
		return "content " + s.ContentID.String()
	case s.Identity != nil:
		return s.Identity.String()
	}
	return "an empty selector"
}

// selectorWire is the stored JSON shape: {"content_id": "<uuid>"} or
// {"identity": "<scheme>:<digest>"}.
type selectorWire struct {
	ContentID *string `json:"content_id,omitempty"`
	Identity  *string `json:"identity,omitempty"`
	// Name is decoded only to be refused by name, so the message can say
	// what the writer tried rather than "unknown field".
	Name *string `json:"name,omitempty"`
}

var renderedIdentity = regexp.MustCompile(`^(pack-jcs-sha256-v1):([0-9a-f]{64})$`)

// ParsePromptSelector decodes a stored selector value.
//
// The rendered identity admits only the plane's own scheme: a selector under
// the legacy scheme could never resolve, since the plane holds no content
// under it, and refusing it here says why.
func ParsePromptSelector(raw []byte) (PromptSelector, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		// A bare JSON string is the shape a name arrives in.
		return PromptSelector{}, fmt.Errorf("%w (got the string %s)", ErrPromptSelectorIsAName, trimmed)
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var wire selectorWire
	if err := decoder.Decode(&wire); err != nil {
		return PromptSelector{}, fmt.Errorf("prompt pack selector is not an object with content_id or identity: %w", err)
	}
	// EOF, not More(): More reports another element of the CURRENT array or
	// object and says nothing about bytes after the top-level value, so
	// `{...}]` would pass it. The same rule the manifest loader applies.
	var trailing json.RawMessage
	if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
		return PromptSelector{}, errors.New("prompt pack selector carries trailing content after the object")
	}
	if wire.Name != nil {
		return PromptSelector{}, fmt.Errorf("%w (got name %q)", ErrPromptSelectorIsAName, *wire.Name)
	}
	switch {
	case wire.ContentID != nil && wire.Identity != nil:
		return PromptSelector{}, errors.New("prompt pack selector names both a content id and an identity; it must name exactly one")
	case wire.ContentID != nil:
		id, err := uuid.Parse(*wire.ContentID)
		if err != nil {
			return PromptSelector{}, fmt.Errorf("prompt pack selector content_id %q is not a UUID: %w", *wire.ContentID, err)
		}
		return PromptSelector{ContentID: &id}, nil
	case wire.Identity != nil:
		match := renderedIdentity.FindStringSubmatch(*wire.Identity)
		if match == nil {
			return PromptSelector{}, fmt.Errorf("prompt pack selector identity %q is not of the form %s:<64 lowercase hex>",
				*wire.Identity, PromptSchemePackJCS)
		}
		return PromptSelector{Identity: &PromptIdentity{Scheme: PromptScheme(match[1]), Digest: match[2]}}, nil
	}
	return PromptSelector{}, errors.New("prompt pack selector names neither a content id nor an identity")
}

// MarshalJSON writes the wire shape ParsePromptSelector reads.
func (s PromptSelector) MarshalJSON() ([]byte, error) {
	var wire selectorWire
	switch {
	case s.ContentID != nil && s.Identity != nil:
		return nil, errors.New("prompt pack selector names both a content id and an identity")
	case s.ContentID != nil:
		text := s.ContentID.String()
		wire.ContentID = &text
	case s.Identity != nil:
		text := s.Identity.String()
		wire.Identity = &text
	default:
		return nil, errors.New("prompt pack selector is empty")
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode prompt pack selector: %w", err)
	}
	return raw, nil
}

// PromptSelectorSchema is the configkeys validator for PromptPackKey: the
// stored bytes must parse as a selector.
func PromptSelectorSchema() configkeys.Validator {
	return configkeys.ValidatorFunc(func(value []byte) error {
		_, err := ParsePromptSelector(value)
		return err
	})
}
