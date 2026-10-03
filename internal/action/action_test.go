package action_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/action"
)

// TestCallValidateRefusesWhatAnExecutorCannotActOn pins the three refusals
// and the positive control. The version check is the one that matters: a
// uuid.New() is a valid uuid and an invalid attempt id, and a Validate that
// checked only for uuid.Nil would pass it through to the seam's refusal.
func TestCallValidateRefusesWhatAnExecutorCannotActOn(t *testing.T) {
	v7, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	valid := action.Call{AttemptID: v7, CallerRef: "call_1", Name: "forge/story_pull_request"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid call was refused: %v", err)
	}

	for name, broken := range map[string]action.Call{
		"no attempt id":     {Name: "x"},
		"a v4 attempt id":   {AttemptID: uuid.New(), Name: "x"},
		"no name":           {AttemptID: v7},
		"a whitespace name": {AttemptID: v7, Name: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			err := broken.Validate()
			if !errors.Is(err, action.ErrInvalidCall) {
				t.Fatalf("err = %v, want ErrInvalidCall", err)
			}
		})
	}
}
