package readiness

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRefuseCarriesCauseDetailRemedyAndChain(t *testing.T) {
	inner := errors.New("the producer's own sentinel")
	err := Refuse(SchemaBehind, "version 3, binary 5", "migrate", inner)

	if cause, ok := CauseOf(fmt.Errorf("wrapped: %w", err)); !ok || cause != SchemaBehind {
		t.Fatalf("CauseOf = %q, %v; want %q through a wrap", cause, ok, SchemaBehind)
	}
	if remedy, ok := RemedyOf(err); !ok || remedy != "migrate" {
		t.Fatalf("RemedyOf = %q, %v", remedy, ok)
	}
	if !errors.Is(err, inner) {
		t.Fatal("the producer's error is not in the chain; a caller below the seam could no longer errors.Is it")
	}
	for _, want := range []string{"schema_behind", "version 3, binary 5", "Remedy: migrate", inner.Error()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rendering lacks %q: %s", want, err)
		}
	}
}

func TestCauseOfIsFalseForAnOrdinaryError(t *testing.T) {
	if _, ok := CauseOf(errors.New("plain")); ok {
		t.Fatal("an ordinary error reported a cause")
	}
}

func TestRefusePanicsOnAnUnknownCauseOrEmptyRemedy(t *testing.T) {
	expectPanic(t, "unknown cause", func() { _ = Refuse(Cause("made_up"), "d", "r", nil) })
	expectPanic(t, "empty remedy", func() { _ = Refuse(NoPlane, "d", "", nil) })
}

func TestWithRemedyReplacesOnlyTheRemedyAndKeepsTheChain(t *testing.T) {
	inner := errors.New("sentinel")
	neutral := Refuse(SchemaBehind, "detail", "apply the pending migrations", inner)
	local := WithRemedy(fmt.Errorf("composer: %w", neutral), "make dataplane-migrate")

	cause, _ := CauseOf(local)
	remedy, _ := RemedyOf(local)
	if cause != SchemaBehind || remedy != "make dataplane-migrate" {
		t.Fatalf("cause %q remedy %q", cause, remedy)
	}
	if !errors.Is(local, inner) {
		t.Fatal("re-remedying dropped the chain")
	}
	if got, _ := RemedyOf(neutral); got != "apply the pending migrations" {
		t.Fatal("WithRemedy mutated the original, which another holder may be reading")
	}
	plain := errors.New("no cause")
	if WithRemedy(plain, "x") != plain { //nolint:errorlint // identity is the point: nothing may be wrapped
		t.Fatal("an error without a cause was wrapped")
	}
}

func TestEveryCauseIsKnownAndDistinct(t *testing.T) {
	seen := map[Cause]bool{}
	for _, c := range Causes {
		if !c.Known() {
			t.Fatalf("%q is listed and not Known", c)
		}
		if seen[c] {
			t.Fatalf("%q listed twice", c)
		}
		seen[c] = true
	}
	if Cause("").Known() {
		t.Fatal("the empty cause is Known")
	}
}

func expectPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s did not panic", what)
		}
	}()
	fn()
}
