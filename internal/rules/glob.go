package rules

import (
	"path/filepath"
	"strings"
)

// matchGlob reports whether path matches pattern, where "**" spans zero or
// more path segments and every other segment is matched by filepath.Match.
//
// filepath.Match has no "**", and the config path list needs it, so this is
// the smallest thing that does the job rather than a fifth dependency.
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}

	if pattern[0] == "**" {
		// try every split point, including consuming nothing.
		for i := 0; i <= len(path); i++ {
			if matchSegments(pattern[1:], path[i:]) {
				return true
			}
		}

		return false
	}

	if len(path) == 0 {
		return false
	}

	ok, err := filepath.Match(pattern[0], path[0])
	if err != nil || !ok {
		return false
	}

	return matchSegments(pattern[1:], path[1:])
}

// hasGlobMeta reports whether a path contains glob metacharacters, in which
// case it names an unknown set of files rather than one file.
func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}
