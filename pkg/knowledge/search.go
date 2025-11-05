package knowledge

import (
	"regexp"
	"sort"
	"strings"
)

// ExtractKeyTerms extracts search terms from story content.
// Returns space-separated string of top 20 terms by frequency.
func ExtractKeyTerms(description string, acceptanceCriteria []string) string {
	// 1. Combine all text
	text := description + " " + strings.Join(acceptanceCriteria, " ")

	// 2. Tokenize on whitespace and punctuation, preserve identifiers
	// Pattern preserves camelCase, snake_case, kebab-case identifiers
	tokens := regexp.MustCompile(`[a-zA-Z0-9_-]+`).FindAllString(text, -1)

	// 3. Filter stop words (common English words)
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"as": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "being": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true,
		"would": true, "should": true, "could": true, "may": true, "might": true,
		"must": true, "can": true, "this": true, "that": true, "these": true,
		"those": true, "i": true, "you": true, "he": true, "she": true,
		"it": true, "we": true, "they": true, "what": true, "which": true,
		"who": true, "when": true, "where": true, "why": true, "how": true,
	}

	// 4. Build frequency map (case-insensitive)
	freq := make(map[string]int)
	for _, token := range tokens {
		lower := strings.ToLower(token)
		if len(lower) < 3 { // Skip very short words
			continue
		}
		if stopWords[lower] {
			continue
		}
		freq[token]++ // Keep original case
	}

	// 5. Sort by frequency, take top 20
	type termFreq struct {
		term string
		freq int
	}
	sorted := make([]termFreq, 0, len(freq))
	for term, f := range freq {
		sorted = append(sorted, termFreq{term, f})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].freq > sorted[j].freq
	})

	maxTerms := 20
	if len(sorted) > maxTerms {
		sorted = sorted[:maxTerms]
	}

	terms := make([]string, len(sorted))
	for i, tf := range sorted {
		terms[i] = tf.term
	}

	return strings.Join(terms, " ")
}
