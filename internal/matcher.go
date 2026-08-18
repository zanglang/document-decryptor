package internal

import "strings"

// Match represents a single configured profile that matched at least one
// supplied identifier.
type Match struct {
	ProfileName string
	Pattern     string
	Profile     Profile
}

// FindMatches performs a case-insensitive substring search of every
// pattern in every configured profile against every supplied identifier. A
// profile is included in the result at most once, even if multiple of its
// patterns match, or one pattern matches multiple identifiers; Pattern is
// set to the first configured pattern (in list order) that matched.
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
	for name, profile := range cfg {
		for _, pattern := range profile.Patterns {
			lowerPattern := strings.ToLower(pattern)
			matched := false
			for _, id := range lowerIdentifiers {
				if strings.Contains(id, lowerPattern) {
					matched = true
					break
				}
			}
			if matched {
				matches = append(matches, Match{ProfileName: name, Pattern: pattern, Profile: profile})
				break
			}
		}
	}

	return matches
}
