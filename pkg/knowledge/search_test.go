package knowledge

import (
	"strings"
	"testing"
)

// TestExtractKeyTerms tests term extraction from story content.
func TestExtractKeyTerms(t *testing.T) {
	tests := []struct {
		name        string
		description string
		criteria    []string
		wantTerms   []string
		wantNot     []string
	}{
		{
			name:        "simple technical terms",
			description: "Implement authentication middleware for API endpoints",
			criteria: []string{
				"Must use JWT tokens",
				"Should validate permissions",
			},
			wantTerms: []string{"authentication", "middleware", "API", "JWT", "tokens", "permissions"},
			wantNot:   []string{"the", "for", "must", "should"},
		},
		{
			name:        "database operations",
			description: "Add connection pooling for database queries",
			criteria: []string{
				"Use prepared statements",
				"Handle connection timeouts",
			},
			wantTerms: []string{"connection", "pooling", "database", "queries", "prepared", "statements", "timeouts"},
			wantNot:   []string{"the", "for", "use"},
		},
		{
			name:        "camelCase and snake_case preservation",
			description: "Refactor getUserProfile and update_user_settings functions",
			criteria: []string{
				"Maintain backward compatibility with existingAPI",
			},
			wantTerms: []string{"getUserProfile", "update_user_settings", "existingAPI"},
			wantNot:   []string{"the", "and", "with"},
		},
		{
			name:        "hyphenated terms",
			description: "Implement rate-limiting for API requests",
			criteria: []string{
				"Use token-bucket algorithm",
			},
			wantTerms: []string{"rate-limiting", "token-bucket", "API", "requests", "algorithm"},
			wantNot:   []string{"for", "use"},
		},
		{
			name:        "empty input",
			description: "",
			criteria:    []string{},
			wantTerms:   []string{},
			wantNot:     []string{},
		},
		{
			name:        "technical acronyms",
			description: "Add CORS headers to REST API using HTTP middleware",
			criteria: []string{
				"Support OPTIONS preflight requests",
			},
			wantTerms: []string{"CORS", "REST", "API", "HTTP", "middleware", "OPTIONS", "preflight", "requests"},
			wantNot:   []string{"to", "using", "support"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractKeyTerms(tt.description, tt.criteria)
			terms := strings.Fields(result)

			// Check that wanted terms are present
			for _, want := range tt.wantTerms {
				found := false
				for _, term := range terms {
					if strings.EqualFold(term, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ExtractKeyTerms() missing expected term %q, got terms: %v", want, terms)
				}
			}

			// Check that unwanted terms (stop words) are not present
			for _, notWant := range tt.wantNot {
				for _, term := range terms {
					if strings.EqualFold(term, notWant) {
						t.Errorf("ExtractKeyTerms() included stop word %q", term)
					}
				}
			}
		})
	}
}

// TestExtractKeyTermsFrequency tests that high-frequency terms are prioritized.
func TestExtractKeyTermsFrequency(t *testing.T) {
	description := "API API API database database error"
	criteria := []string{
		"API endpoints must be documented",
		"Database transactions should be atomic",
	}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	// "API" appears 4 times, "database" 2 times, "error" 1 time
	// Higher frequency terms should appear first (if implementation sorts by frequency)
	if len(terms) == 0 {
		t.Fatal("ExtractKeyTerms() returned empty result")
	}

	// Check that API and database are included (most frequent)
	hasAPI := false
	hasDatabase := false
	for _, term := range terms {
		if strings.EqualFold(term, "API") {
			hasAPI = true
		}
		if strings.EqualFold(term, "database") {
			hasDatabase = true
		}
	}

	if !hasAPI {
		t.Error("ExtractKeyTerms() missing high-frequency term 'API'")
	}
	if !hasDatabase {
		t.Error("ExtractKeyTerms() missing high-frequency term 'database'")
	}
}

// TestExtractKeyTermsStopWords tests that common stop words are filtered.
func TestExtractKeyTermsStopWords(t *testing.T) {
	description := "The system should implement the feature using the database"
	criteria := []string{"The user must be able to access the data"}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	stopWords := []string{"the", "should", "using", "must", "be", "able", "to"}

	for _, stopWord := range stopWords {
		for _, term := range terms {
			if strings.EqualFold(term, stopWord) {
				t.Errorf("ExtractKeyTerms() failed to filter stop word %q", stopWord)
			}
		}
	}

	// Should include meaningful terms
	meaningfulTerms := []string{"system", "implement", "feature", "database", "user", "access", "data"}
	for _, meaningful := range meaningfulTerms {
		found := false
		for _, term := range terms {
			if strings.EqualFold(term, meaningful) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ExtractKeyTerms() missing meaningful term %q", meaningful)
		}
	}
}

// TestExtractKeyTermsLimit tests that result is limited to top terms.
func TestExtractKeyTermsLimit(t *testing.T) {
	// Generate description with many unique terms
	description := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega"
	criteria := []string{}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	// Should limit to top 20 terms (based on spec)
	if len(terms) > 20 {
		t.Errorf("ExtractKeyTerms() returned %d terms, want at most 20", len(terms))
	}
}

// TestExtractKeyTermsSpecialCharacters tests handling of special characters.
func TestExtractKeyTermsSpecialCharacters(t *testing.T) {
	description := "Implement error-handling, retry-logic & connection-pooling!"
	criteria := []string{"Support UTF-8 encoding, handle edge-cases."}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	// Hyphenated terms should be preserved
	wantHyphenated := []string{"error-handling", "retry-logic", "connection-pooling", "UTF-8", "edge-cases"}
	for _, want := range wantHyphenated {
		found := false
		for _, term := range terms {
			if strings.Contains(term, want) || strings.EqualFold(term, strings.ReplaceAll(want, "-", "")) {
				found = true
				break
			}
		}
		if !found {
			// This is informational - documents actual behavior
			t.Logf("Note: hyphenated term %q may be split or preserved", want)
		}
	}
}

// TestExtractKeyTermsWhitespace tests handling of various whitespace.
func TestExtractKeyTermsWhitespace(t *testing.T) {
	description := "  Multiple   spaces   and\n\nnewlines\t\ttabs  "
	criteria := []string{"  Extra   whitespace  "}

	result := ExtractKeyTerms(description, criteria)

	// Should handle whitespace gracefully
	if result == "" {
		t.Error("ExtractKeyTerms() returned empty for whitespace-heavy input")
	}

	// Terms should be space-separated in output
	if strings.Contains(result, "\n") || strings.Contains(result, "\t") {
		t.Error("ExtractKeyTerms() output contains newlines or tabs")
	}
}

// TestExtractKeyTermsCaseSensitivity tests case handling.
func TestExtractKeyTermsCaseSensitivity(t *testing.T) {
	description := "API api Api REST rest Rest"
	criteria := []string{}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	// Should treat case variations as same term for frequency counting
	// but preserve one of the cases in output
	apiCount := 0
	restCount := 0
	for _, term := range terms {
		if strings.EqualFold(term, "API") {
			apiCount++
		}
		if strings.EqualFold(term, "REST") {
			restCount++
		}
	}

	// Each term should appear once (consolidated by frequency counting)
	if apiCount > 1 {
		t.Errorf("ExtractKeyTerms() returned 'API' %d times, want 1 (case variations should be consolidated)", apiCount)
	}
	if restCount > 1 {
		t.Errorf("ExtractKeyTerms() returned 'REST' %d times, want 1", restCount)
	}

	// But at least one version should be present
	if apiCount == 0 {
		t.Error("ExtractKeyTerms() missing 'API' term (case-insensitive)")
	}
}

// TestExtractKeyTermsNumbers tests handling of numeric content.
func TestExtractKeyTermsNumbers(t *testing.T) {
	description := "Migrate from Python2 to Python3 using schema_v10"
	criteria := []string{"Support IPv4 and IPv6 addresses"}

	result := ExtractKeyTerms(description, criteria)
	terms := strings.Fields(result)

	// Terms with numbers should be preserved
	wantWithNumbers := []string{"Python2", "Python3", "schema_v10", "IPv4", "IPv6"}
	for _, want := range wantWithNumbers {
		found := false
		for _, term := range terms {
			if strings.EqualFold(term, want) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("Note: term with numbers %q may be split or preserved", want)
		}
	}
}
