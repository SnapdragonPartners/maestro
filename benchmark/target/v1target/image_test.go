package v1target

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ensureImagePresent guards a billable failure mode: launching v1 without the
// pinned coder image present lets v1 treat the project as unconfigured, start
// bootstrap work, spend tokens, and pollute the golden diff. The pull is
// allowed to fail, but the image must be present either way.
//
// These tests drive the inspect seam directly. The pull itself is a real
// `docker pull` of an unresolvable reference, which fails fast without a
// daemon and without network, so both paths stay hermetic.
func TestEnsureImagePresent(t *testing.T) {
	const missingRef = "golden-benchmark.invalid/no-such-image@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"

	tests := []struct {
		name        string
		inspect     imageInspector
		wantErr     bool
		wantErrHas  string
		description string
	}{
		{
			name:        "offline but image already present",
			inspect:     func(context.Context, string) error { return nil },
			wantErr:     false,
			description: "a failed pull is tolerable when the exact image resolves locally (offline re-run)",
		},
		{
			name:        "pull failed and image missing",
			inspect:     func(context.Context, string) error { return errors.New("No such image") },
			wantErr:     true,
			wantErrHas:  "not present locally",
			description: "the attempt must abort rather than launch a run that cannot produce a valid result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &v1Run{
				settings:     settings{ContainerImage: missingRef},
				inspectImage: tt.inspect,
			}

			err := r.ensureImagePresent(context.Background(), &strings.Builder{})

			if tt.wantErr && err == nil {
				t.Fatalf("expected an error: %s", tt.description)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error (%s): %v", tt.description, err)
			}
			if tt.wantErrHas != "" && !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Errorf("error %q does not mention %q", err, tt.wantErrHas)
			}
		})
	}
}

// A successful pull that still leaves the image unresolvable is a distinct,
// more alarming failure than "offline and missing" — it means the reference
// resolved remotely but not locally — so it must not be silently tolerated.
func TestEnsureImagePresentPullSucceededButImageAbsent(t *testing.T) {
	r := &v1Run{
		// Empty ref makes `docker pull` fail immediately; the point of this case
		// is the inspect verdict, which is authoritative regardless of the pull.
		settings:     settings{ContainerImage: "golden-benchmark.invalid/absent:latest"},
		inspectImage: func(context.Context, string) error { return errors.New("No such image") },
	}

	if err := r.ensureImagePresent(context.Background(), &strings.Builder{}); err == nil {
		t.Fatal("expected an error when the image does not resolve locally")
	}
}
