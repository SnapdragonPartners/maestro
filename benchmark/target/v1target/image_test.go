package v1target

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ensureImagePresent guards a billable failure mode: launching v1 without the
// pinned coder image present lets v1 treat the project as unconfigured, start
// bootstrap work, spend tokens, and pollute the golden diff.
//
// Every case below is fully hermetic — both docker calls are stubbed, so no
// test contacts a daemon, a registry, or the network.

const testImageRef = "golden-benchmark.invalid/fixture@sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// Mirrors docker's wording; lowercased to satisfy Go error-string style.
var errNoSuchImage = errors.New("no such image")

// scriptedInspect returns an inspector whose Nth call yields results[N],
// recording how many times it ran. Lets a test say "missing, then present".
func scriptedInspect(counter *int, results ...error) imageInspector {
	return func(context.Context, string) error {
		i := *counter
		*counter++
		if i < len(results) {
			return results[i]
		}
		return results[len(results)-1]
	}
}

func TestEnsureImagePresent(t *testing.T) {
	tests := []struct {
		name        string
		inspects    []error // successive inspect results
		pullErr     error
		wantErr     string // substring; empty means success expected
		wantPulls   int
		description string
	}{
		{
			name:        "already present skips the pull",
			inspects:    []error{nil},
			wantPulls:   0,
			description: "images are digest-pinned, so a local hit is already the exact bytes",
		},
		{
			name:        "missing then pulled successfully",
			inspects:    []error{errNoSuchImage, nil},
			pullErr:     nil,
			wantPulls:   1,
			description: "the normal cold path: absent locally, fetched, then verified",
		},
		{
			name:        "offline but image present on re-inspect",
			inspects:    []error{errNoSuchImage, nil},
			pullErr:     errors.New("network unreachable"),
			wantPulls:   1,
			description: "a failed pull is tolerable when the image resolves locally anyway",
		},
		{
			name:        "pull failed and image missing",
			inspects:    []error{errNoSuchImage, errNoSuchImage},
			pullErr:     errors.New("network unreachable"),
			wantErr:     "not present locally",
			wantPulls:   1,
			description: "must abort rather than launch a run that cannot produce a valid result",
		},
		{
			name:        "pull succeeded but image still absent",
			inspects:    []error{errNoSuchImage, errNoSuchImage},
			pullErr:     nil,
			wantErr:     "not present after a successful pull",
			wantPulls:   1,
			description: "resolved remotely but not locally is a distinct, alarming failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inspectCalls, pullCalls := 0, 0
			r := &v1Run{
				settings:     settings{ContainerImage: testImageRef},
				inspectImage: scriptedInspect(&inspectCalls, tt.inspects...),
				pullImage: func(context.Context, string) error {
					pullCalls++
					return tt.pullErr
				},
			}

			err := r.ensureImagePresent(context.Background(), &strings.Builder{})

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error (%s): %v", tt.description, err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected an error (%s)", tt.description)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}

			if pullCalls != tt.wantPulls {
				t.Errorf("pull called %d times, want %d — %s", pullCalls, tt.wantPulls, tt.description)
			}
		})
	}
}

// The aborting path must surface BOTH causes: why the fetch failed and that the
// image is absent. Losing either turns a diagnosable failure into a guess.
func TestEnsureImagePresentErrorReportsBothCauses(t *testing.T) {
	pullFailure := errors.New("registry timeout")
	inspectCalls := 0
	r := &v1Run{
		settings:     settings{ContainerImage: testImageRef},
		inspectImage: scriptedInspect(&inspectCalls, errNoSuchImage, errNoSuchImage),
		pullImage:    func(context.Context, string) error { return pullFailure },
	}

	err := r.ensureImagePresent(context.Background(), &strings.Builder{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, pullFailure) {
		t.Errorf("error does not wrap the pull failure: %v", err)
	}
	if !errors.Is(err, errNoSuchImage) {
		t.Errorf("error does not wrap the inspect failure: %v", err)
	}
	if !strings.Contains(err.Error(), testImageRef) {
		t.Errorf("error does not name the image: %v", err)
	}
}
