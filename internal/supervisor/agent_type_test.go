package supervisor

import "testing"

// P-2 regression: restart must recover the agent type from the ID when the
// unexpected-exit cleanup already deleted the tracking entry.
func TestAgentTypeFromID(t *testing.T) {
	cases := map[string]string{
		"coder-001":     "coder",
		"coder-002":     "coder",
		"hotfix-001":    "coder",
		"architect-001": "architect",
		"pm-001":        "pm",
		"mystery-001":   "",
		"nodash":        "",
	}
	for id, want := range cases {
		if got := agentTypeFromID(id); got != want {
			t.Errorf("agentTypeFromID(%q) = %q, want %q", id, got, want)
		}
	}
}
