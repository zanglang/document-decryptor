package internal

import "strings"

// Match represents a single configured pattern that matched at least one
// supplied identifier.
type Match struct {
	Pattern string
	Profile Profile
}

// FindMatches performs a case-insensitive substring search of every
// configured pattern against every supplied identifier. A pattern is
// included in the result at most once, even if it matches multiple
// identifiers.
//
// Matching is deliberately simple: no regular expressions, no fuzzy
// matching. strings.ToLower is used for case folding, which handles
// Unicode simple case folding for the vast majority of scripts without
// pulling in additional dependencies.
func FindMatches(cfg PatternConfig, identifiers []string) []Match {
	lowerIdentifiers := make([]string, len(identifiers))
	for i, id := range identifiers {
		lowerIdentifiers[i] = strings.ToLower(id)
	}

	var matches []Match
	for pattern, profile := range cfg {
		lowerPattern := strings.ToLower(pattern)
		for _, id := range lowerIdentifiers {
			if strings.Contains(id, lowerPattern) {
				matches = append(matches, Match{Pattern: pattern, Profile: profile})
				break
			}
		}
	}

	return matches
}
