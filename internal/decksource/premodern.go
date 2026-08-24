package decksource

import (
	"regexp"
	"strings"
)

var (
	ruleRE      = regexp.MustCompile(`^[-=_*]{3,}$`)
	bannerRE    = regexp.MustCompile(`(?i)^deck downloaded from\b`)
	groupRE     = regexp.MustCompile(`^[A-Za-z][A-Za-z '&/-]*:\s*\d+$`)
	headCountRE = regexp.MustCompile(`\s*[({\[]\s*\d+\s*[)}\]]$`)
)

func structuralNoise(line string) bool {
	return ruleRE.MatchString(line) || bannerRE.MatchString(line) || groupRE.MatchString(line)
}

func declaredTitle(lines []string) string {
	var prev string
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if _, ok := sectionHeaders[headerKey(line)]; ok {
			return ""
		}
		if _, ok := parseLine(line); ok {
			return ""
		}
		if ruleRE.MatchString(prev) && !structuralNoise(line) && ruleRE.MatchString(nextSolid(lines, i)) {
			return line
		}
		prev = line
	}
	return ""
}

func nextSolid(lines []string, from int) string {
	for _, raw := range lines[from+1:] {
		if line := strings.TrimSpace(raw); line != "" {
			return line
		}
	}
	return ""
}
