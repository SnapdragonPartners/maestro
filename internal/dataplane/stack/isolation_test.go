package stack

import (
	"errors"
	"strings"
	"testing"
)

// The project-name override becomes a command-line argument selecting which
// containers an operation destroys, so it is validated rather than trusted.
func TestProjectNameOverride(t *testing.T) {
	tests := map[string]struct {
		value   string
		want    string
		wantErr bool
	}{
		"unset keeps the default":  {value: "", want: DefaultProjectName},
		"a valid name is taken":    {value: "maestro-test-a1b2", want: "maestro-test-a1b2"},
		"underscores are fine":     {value: "maestro_test_1", want: "maestro_test_1"},
		"a leading dash is a flag": {value: "-rf", wantErr: true},
		"uppercase is rejected":    {value: "Maestro", wantErr: true},
		"spaces are rejected":      {value: "maestro test", wantErr: true},
		"empty is rejected":        {value: " ", wantErr: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.value != "" {
				t.Setenv(EnvProjectName, test.value)
			}
			resolved := DefaultProjectName
			err := applyProjectOverride(&resolved)
			switch {
			case test.wantErr && !errors.Is(err, ErrInvalidProjectName):
				t.Errorf("err = %v, want the override refused", err)
			case !test.wantErr && err != nil:
				t.Errorf("err = %v, want the override taken", err)
			case !test.wantErr && resolved != test.want:
				t.Errorf("project = %q, want %q", resolved, test.want)
			}
		})
	}
}

// A leading dash is the case worth its own assertion: Compose would read it
// as a flag rather than as a project, so the operation would act on
// something other than what the caller named.
func TestProjectNameRejectsFlagLikeValues(t *testing.T) {
	t.Setenv(EnvProjectName, "--project-name=maestro-dataplane")

	resolved := DefaultProjectName
	err := applyProjectOverride(&resolved)
	if !errors.Is(err, ErrInvalidProjectName) {
		t.Fatalf("err = %v, want the override refused", err)
	}
	if resolved != DefaultProjectName {
		t.Errorf("project = %q, want the default left alone on a refusal", resolved)
	}
	if !strings.Contains(err.Error(), EnvProjectName) {
		t.Errorf("refusal does not name the variable to fix: %v", err)
	}
}
