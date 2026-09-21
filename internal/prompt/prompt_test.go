package prompt

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Fixture vocabulary. This package registers no production slots (design
// D1), so every test brings its own, as configkeys' tests do.
const (
	slotCoderSystem  SlotKey = "coder.system"
	slotCoderPlan    SlotKey = "coder.plan"
	slotReviewSystem SlotKey = "review.system"
)

func fixtureRegistry(t *testing.T) *Registry {
	t.Helper()
	built, err := New(map[SlotKey]Slot{
		slotCoderSystem:  {Roles: []Role{"coder"}, Variables: []Variable{"Story", "Workspace"}},
		slotCoderPlan:    {Roles: []Role{"coder"}, Variables: []Variable{"Story"}},
		slotReviewSystem: {Roles: []Role{"architect", "reviewer"}},
	})
	if err != nil {
		t.Fatalf("fixture registry: %v", err)
	}
	return built
}

// validCoderPack is the positive control every refusal below is paired with:
// the same call with the fault removed passes, so a refusal caused by some
// unrelated rule is distinguishable from the one under test.
func validCoderPack() map[string]string {
	return map[string]string{
		string(slotCoderSystem): "You work in {{.Workspace}}.\n{{if .Story}}Story: {{.Story}}{{end}}\n",
		string(slotCoderPlan):   "Plan {{.Story}}.",
	}
}

func TestNewRefusesMalformedRegistrations(t *testing.T) {
	tests := []struct {
		name    string
		key     SlotKey
		slot    Slot
		wantMsg string
	}{
		{"empty key", "", Slot{Roles: []Role{"coder"}}, "canonical dotted name"},
		{"uppercase key", "Coder.System", Slot{Roles: []Role{"coder"}}, "canonical dotted name"},
		{"trailing dot", "coder.", Slot{Roles: []Role{"coder"}}, "canonical dotted name"},
		{"no role", "coder.system", Slot{}, "names no role"},
		{"blank role", "coder.system", Slot{Roles: []Role{""}}, "canonical role name"},
		{"dotted role", "coder.system", Slot{Roles: []Role{"coder.lead"}}, "canonical role name"},
		{"duplicate role", "coder.system", Slot{Roles: []Role{"coder", "coder"}}, `lists role "coder" twice`},
		{"lowercase variable", "coder.system", Slot{Roles: []Role{"coder"}, Variables: []Variable{"story"}}, "could not reference"},
		{"dotted variable", "coder.system", Slot{Roles: []Role{"coder"}, Variables: []Variable{"Story.ID"}}, "could not reference"},
		{"duplicate variable", "coder.system", Slot{Roles: []Role{"coder"}, Variables: []Variable{"Story", "Story"}}, `lists variable "Story" twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(map[SlotKey]Slot{tt.key: tt.slot})
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("New() error = %v, want one containing %q", err, tt.wantMsg)
			}
		})
	}
}

// TestRegistryIsFrozen: a caller keeping the slices it registered with must
// not be able to rewrite the contract afterwards.
func TestRegistryIsFrozen(t *testing.T) {
	variables := []Variable{"Story"}
	roles := []Role{"coder"}
	built, err := New(map[SlotKey]Slot{slotCoderPlan: {Roles: roles, Variables: variables}})
	if err != nil {
		t.Fatal(err)
	}
	variables[0] = "Secret"
	roles[0] = "architect"

	if got := built.Roles(); !slices.Equal(got, []Role{"coder"}) {
		t.Fatalf("Roles() = %v after the caller's slice was rewritten", got)
	}
	if _, err := built.Render(slotCoderPlan, "{{.Story}}", map[Variable]string{"Story": "s"}); err != nil {
		t.Fatalf("the registered variable stopped rendering: %v", err)
	}
}

func TestVocabularyListings(t *testing.T) {
	r := fixtureRegistry(t)
	if got, want := r.Slots(), []SlotKey{slotCoderPlan, slotCoderSystem, slotReviewSystem}; !slices.Equal(got, want) {
		t.Errorf("Slots() = %v, want %v", got, want)
	}
	if got, want := r.Roles(), []Role{"architect", "coder", "reviewer"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %v, want %v", got, want)
	}
}

func TestValidatePackAcceptsTheControls(t *testing.T) {
	r := fixtureRegistry(t)
	tests := []struct {
		name    string
		entries map[string]string
		roles   []string
	}{
		{"declared and covered", validCoderPack(), []string{"coder"}},
		{"roles are a set", validCoderPack(), []string{"coder", "coder"}},
		{"entries beyond the declared roles", map[string]string{string(slotCoderPlan): "Plan {{.Story}}."}, nil},
		{"one slot satisfies either of its roles", map[string]string{string(slotReviewSystem): "Review."}, []string{"reviewer"}},
		{"the empty pack, which is the built-in at item 4", map[string]string{}, nil},
		{"nil entries", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := r.ValidatePack(tt.entries, tt.roles); err != nil {
				t.Fatalf("ValidatePack() = %v, want nil", err)
			}
		})
	}
}

// TestValidatePackRefusals holds design D10's three deliberate faults -- a
// missing slot for a declared role, an unparseable entry, a reference to a
// variable the slot does not supply -- and the rules around them. Each case
// is validCoderPack with one thing wrong.
func TestValidatePackRefusals(t *testing.T) {
	r := fixtureRegistry(t)
	with := func(key SlotKey, text string) map[string]string {
		pack := validCoderPack()
		pack[string(key)] = text
		return pack
	}
	without := func(key SlotKey) map[string]string {
		pack := validCoderPack()
		delete(pack, string(key))
		return pack
	}

	tests := []struct {
		name    string
		entries map[string]string
		roles   []string
		want    error
		wantMsg string
	}{
		{"unregistered slot", with("coder.review", "x"), []string{"coder"}, ErrUnknownSlot, `"coder.review"`},
		{"unknown role", validCoderPack(), []string{"coder", "pm"}, ErrUnknownRole, `"pm"`},
		{"blank role", validCoderPack(), []string{""}, ErrUnknownRole, `""`},
		{"declared role missing a slot", without(slotCoderPlan), []string{"coder"}, ErrMissingSlot, `role "coder" requires slot "coder.plan"`},
		{"declared role with no entries at all", map[string]string{}, []string{"architect"}, ErrMissingSlot, `"review.system"`},
		{"blank entry", with(slotCoderPlan, " \n\t"), []string{"coder"}, ErrInvalidEntry, "blank"},
		{"invalid UTF-8", with(slotCoderPlan, "plan \xff"), []string{"coder"}, ErrInvalidEntry, "UTF-8"},
		{"NUL", with(slotCoderPlan, "plan \x00"), []string{"coder"}, ErrInvalidEntry, "NUL"},
		{"unparseable", with(slotCoderPlan, "Plan {{.Story"), []string{"coder"}, ErrParse, "unclosed action"},
		{"unterminated if", with(slotCoderPlan, "{{if .Story}}x"), []string{"coder"}, ErrParse, "unexpected EOF"},
		{"unknown function", with(slotCoderPlan, "{{upper .Story}}"), []string{"coder"}, ErrParse, "not defined"},
		{"undeclared variable", with(slotCoderPlan, "Plan {{.Workspace}}."), []string{"coder"}, ErrUndeclaredVariable, `"Workspace"`},
		{"undeclared variable in an untaken branch", with(slotCoderPlan, `{{if eq .Story "never"}}{{.Secret}}{{end}}x`), []string{"coder"}, ErrUndeclaredVariable, `"Secret"`},
		{"undeclared variable in a condition", with(slotCoderPlan, "{{if and .Story (not .Secret)}}x{{end}}y"), []string{"coder"}, ErrUndeclaredVariable, `"Secret"`},
		{"an entry outside the declared roles is still checked", map[string]string{string(slotCoderPlan): "{{.Secret}}"}, nil, ErrUndeclaredVariable, `"Secret"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.ValidatePack(tt.entries, tt.roles)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidatePack() = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("ValidatePack() = %q, want it to name %s", err, tt.wantMsg)
			}
		})
	}
}

// TestDialectRefusals: each construct the dialect excludes, refused as
// ErrDialect rather than reaching the renderer. Every text here parses.
func TestDialectRefusals(t *testing.T) {
	r := fixtureRegistry(t)
	tests := []struct {
		name    string
		text    string
		wantMsg string
	}{
		{"range", "{{range .Story}}x{{end}}", "rebind dot"},
		{"with", "{{with .Story}}{{.Workspace}}{{end}}", "rebind dot"},
		{"define beside text", `{{define "extra"}}x{{end}}body`, `defines template "extra"`},
		{"define that names its own slot", `{{define "coder.system"}}{{.Story}}{{end}}`, `defines template "coder.system"`},
		{"template call", `{{template "other"}}`, "may not invoke another"},
		{"block", `{{block "other" .}}x{{end}}`, "defines template"},
		{"declaration", "{{$s := .Story}}x", "declarations"},
		{"root variable", "{{$.Story}}", "operand"},
		{"pipeline", "{{.Story | not}}", "pipelines"},
		{"bare dot", "{{.}}", "operand"},
		{"nested field", "{{.Story.Title}}", "one level deep"},
		{"chain", "{{(.Story).Title}}", "operand"},
		{"printf", `{{printf "%s" .Story}}`, `"printf" is not admitted`},
		{"index", "{{index .Story 0}}", `"index" is not admitted`},
		{"call", "{{call .Story}}", `"call" is not admitted`},
		{"ordering comparison", `{{if lt .Story "b"}}x{{end}}`, `"lt" is not admitted`},
		{"number literal", "{{eq .Story 1}}", "operand"},
		{"bool literal", "{{and .Story true}}", "operand"},
		{"nil literal", "{{eq .Story nil}}", "operand"},
		{"comparison of an expression", `{{if eq .Story (not .Workspace)}}x{{end}}`, "comparison takes"},
		{"field given arguments", `{{.Story "x"}}`, "only a function takes arguments"},
		{"not with two arguments", "{{not .Story .Workspace}}", `arguments for "not"`},
		{"eq with one argument", "{{eq .Story}}", `arguments for "eq"`},
		{"ne with three arguments", `{{ne .Story "a" "b"}}`, `arguments for "ne"`},
		{"and with no arguments", "{{and}}", `arguments for "and"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pack := validCoderPack()
			pack[string(slotCoderSystem)] = tt.text
			err := r.ValidatePack(pack, []string{"coder"})
			if !errors.Is(err, ErrDialect) {
				t.Fatalf("ValidatePack() = %v, want ErrDialect", err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("ValidatePack() = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

// TestAdmittedDialectRendersOnEveryPath backs the claim the dialect's
// narrowness rests on: what the walk admits does not fail at render, on
// whichever branch the values select. Import renders one path only, so this
// drives each entry over values chosen to take the others.
func TestAdmittedDialectRendersOnEveryPath(t *testing.T) {
	r := fixtureRegistry(t)
	entries := []string{
		"{{.Story}} in {{.Workspace}}",
		"{{- .Story -}} {{/* a comment */}} tail",
		`{{"literal"}}`,
		"{{if .Story}}a{{else if .Workspace}}b{{else}}c{{end}}",
		`{{if eq .Story "x" "y" .Workspace}}a{{else}}b{{end}}`,
		`{{if ne .Story .Workspace}}a{{else}}b{{end}}`,
		`{{if and .Story (or .Workspace "fallback") (not (eq .Story "x"))}}a{{else}}b{{end}}`,
		`{{or .Story "unnamed"}} {{and .Story .Workspace}} {{not .Story}} {{eq .Story "x"}}`,
		`{{if not (and (ne .Story "") (or (eq .Workspace "x") .Story))}}a{{else}}b{{end}}`,
	}
	valueSets := []map[Variable]string{
		{"Story": "", "Workspace": ""},
		{"Story": "x", "Workspace": ""},
		{"Story": "", "Workspace": "x"},
		{"Story": "x", "Workspace": "x"},
		{"Story": "other", "Workspace": "y"},
	}
	for _, entry := range entries {
		if err := r.ValidatePack(map[string]string{string(slotCoderSystem): entry}, nil); err != nil {
			t.Errorf("ValidatePack(%q) = %v", entry, err)
			continue
		}
		for _, values := range valueSets {
			if _, err := r.Render(slotCoderSystem, entry, values); err != nil {
				t.Errorf("Render(%q, %v) = %v", entry, values, err)
			}
		}
	}
}

func TestRender(t *testing.T) {
	r := fixtureRegistry(t)
	entry := validCoderPack()[string(slotCoderSystem)]

	got, err := r.Render(slotCoderSystem, entry, map[Variable]string{"Story": "S-1", "Workspace": "/work"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "You work in /work.\nStory: S-1\n"; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}

	// Values are text, never template source: a value that looks like an
	// action must arrive in the prompt as written.
	got, err = r.Render(slotCoderPlan, "Plan {{.Story}}.", map[Variable]string{"Story": "{{.Workspace}} <b>&"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Plan {{.Workspace}} <b>&."; got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}

	got, err = r.Render(slotReviewSystem, "Review.", nil)
	if err != nil || got != "Review." {
		t.Fatalf("Render() of a slot with an empty contract = %q, %v", got, err)
	}
}

func TestRenderRefusals(t *testing.T) {
	r := fixtureRegistry(t)
	full := map[Variable]string{"Story": "s", "Workspace": "w"}
	tests := []struct {
		name   string
		key    SlotKey
		entry  string
		values map[Variable]string
		want   error
	}{
		{"unknown slot", "coder.review", "x", nil, ErrUnknownSlot},
		{"a declared variable not supplied", slotCoderSystem, "x", map[Variable]string{"Story": "s"}, ErrVariableSet},
		{"an undeclared variable supplied", slotCoderPlan, "x", full, ErrVariableSet},
		{"values for a slot that declares none", slotReviewSystem, "x", map[Variable]string{"Story": "s"}, ErrVariableSet},
		{"entry outside the contract", slotCoderPlan, "{{.Workspace}}", map[Variable]string{"Story": "s"}, ErrUndeclaredVariable},
		{"entry outside the dialect", slotCoderSystem, "{{range .Story}}x{{end}}", full, ErrDialect},
		{"entry that does not parse", slotCoderSystem, "{{", full, ErrParse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(tt.key, tt.entry, tt.values)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Render() error = %v, want %v", err, tt.want)
			}
			if got != "" {
				t.Fatalf("Render() returned %q beside an error", got)
			}
		})
	}
}

// TestDigestVectors pins pack-jcs-sha256-v1 to values computed outside Go:
//
//	printf '{}' | shasum -a 256
//	printf '{"a.b":"x<y\\n  z","c":"é"}' | shasum -a 256
//
// The second is the RFC 8785 form written by hand -- `<` and `é` literal, the
// newline escaped -- so it fails if Go's HTML-escaping encoder ever reaches
// the hash, and if the entry's whitespace is ever normalised.
func TestDigestVectors(t *testing.T) {
	const emptyObject = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	tests := []struct {
		name    string
		entries map[string]string
		want    string
	}{
		{"empty", map[string]string{}, emptyObject},
		{"nil is the same identity as empty", nil, emptyObject},
		{"content", map[string]string{"c": "é", "a.b": "x<y\n  z"}, "3eb606bd8c2c879511df9d3d7aa12b72bd7a4cde74996181274d8ff1981ce1e2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Digest(tt.entries)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Digest() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestDigestCoversTextByteForByte: ADR 0031 section 1 -- the container is
// canonicalised, the text is not.
func TestDigestCoversTextByteForByte(t *testing.T) {
	base, err := Digest(map[string]string{"coder.plan": "Plan.\n"})
	if err != nil {
		t.Fatal(err)
	}
	for name, entries := range map[string]map[string]string{
		"trailing newline removed": {"coder.plan": "Plan."},
		"leading space added":      {"coder.plan": " Plan.\n"},
		"CRLF":                     {"coder.plan": "Plan.\r\n"},
		"same text, other slot":    {"coder.system": "Plan.\n"},
	} {
		got, err := Digest(entries)
		if err != nil {
			t.Fatal(err)
		}
		if got == base {
			t.Errorf("%s: digest unchanged", name)
		}
	}
}
