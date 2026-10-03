package store

import (
	"errors"
	"regexp"
	"unicode"
)

var ErrSpecPlanReferenceOnly = errors.New("spec/plan must be written in full in the goal field, not as a reference to doc/specs, doc/plans or docs/superpowers")

var specPlanPathPattern = regexp.MustCompile(`(?i)[\w./-]*\b(?:docs?/(?:specs|plans)|docs/superpowers)/[\w./-]*`)

// specPlanReferenceOnly reports whether text is only a pointer to a spec/plan
// file: it names such a path and little else once the path notation is removed.
// ponytail: short text without a path is not detected, and the 200-rune
// threshold is a heuristic; tighten with a real content check if it misfires.
func specPlanReferenceOnly(text string) bool {
	if !specPlanPathPattern.MatchString(text) {
		return false
	}
	rest := specPlanPathPattern.ReplaceAllString(text, "")
	n := 0
	for _, r := range rest {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n < 200
}

func checkSpecPlanNotReferenceOnly(spec, plan string) error {
	if specPlanReferenceOnly(spec) || specPlanReferenceOnly(plan) {
		return ErrSpecPlanReferenceOnly
	}
	return nil
}
