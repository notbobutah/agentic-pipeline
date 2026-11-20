package work

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/config"
)

var acID = regexp.MustCompile(`^(AC\d+)\b`)

// ACIDs extracts "AC1", "AC2", ... from acceptance criteria text.
func ACIDs(criteria []string) []string {
	var ids []string
	for i, c := range criteria {
		if m := acID.FindStringSubmatch(strings.TrimSpace(c)); m != nil {
			ids = append(ids, m[1])
		} else {
			ids = append(ids, fmt.Sprintf("AC%d", i+1))
		}
	}
	return ids
}

// ValidatePlan applies the deterministic checks of docs/14 §5 and the
// approval rules of ADR-0007. It returns problems that make the plan
// unacceptable as proposed; an empty slice means the plan may go to review.
// Open questions are not problems: they block approval, not proposal.
func ValidatePlan(p *Plan, card *Card, cfg *config.Config) []string {
	var probs []string
	inCard := map[string]bool{}
	for _, c := range card.Components {
		inCard[c] = true
	}

	// Repos: only the card's components, mapped to their configured repo.
	for _, r := range p.Repos {
		comp, ok := cfg.Components[r.Component]
		switch {
		case !ok || !inCard[r.Component]:
			probs = append(probs, fmt.Sprintf("repos: component %q is not on the card", r.Component))
		case r.Repo != comp.Repo:
			probs = append(probs, fmt.Sprintf("repos: component %q must map to %s, got %s", r.Component, comp.Repo, r.Repo))
		}
	}

	// Every acceptance criterion is covered by at least one test.
	covered := map[string]bool{}
	for _, t := range p.Tests {
		for _, id := range t.Covers {
			covered[strings.ToUpper(strings.TrimSpace(id))] = true
		}
	}
	for _, id := range ACIDs(card.AcceptanceCriteria) {
		if !covered[id] {
			probs = append(probs, fmt.Sprintf("tests: %s is not covered by any planned test", id))
		}
	}

	// Scope envelope: one per changed component, inside the configured ceiling,
	// never touching forbidden paths; every planned file inside the envelope.
	env := map[string][]string{}
	for _, e := range p.ScopeEnvelope {
		env[e.Component] = e.PathGlobs
		comp, ok := cfg.Components[e.Component]
		if !ok {
			probs = append(probs, fmt.Sprintf("scopeEnvelope: unknown component %q", e.Component))
			continue
		}
		for _, g := range e.PathGlobs {
			if !GlobWithin(g, comp.Paths) {
				probs = append(probs, fmt.Sprintf("scopeEnvelope: %s glob %q exceeds allowed %v", e.Component, g, comp.Paths))
			}
			// A broad envelope is fine (forbidden paths are always subtracted);
			// an envelope that lies inside a forbidden area is not.
			for _, f := range comp.Forbidden {
				if GlobWithin(g, []string{f}) {
					probs = append(probs, fmt.Sprintf("scopeEnvelope: %s glob %q is inside forbidden %q", e.Component, g, f))
				}
			}
		}
	}
	for _, r := range p.Repos {
		globs, ok := env[r.Component]
		if !ok {
			probs = append(probs, fmt.Sprintf("scopeEnvelope: no envelope for component %q", r.Component))
			continue
		}
		comp := cfg.Components[r.Component]
		for _, ch := range r.Changes {
			if !Match(globs, ch.Path) {
				probs = append(probs, fmt.Sprintf("repos: %s change %q is outside its scope envelope", r.Component, ch.Path))
			}
			if Match(comp.Forbidden, ch.Path) {
				probs = append(probs, fmt.Sprintf("repos: %s change %q touches a forbidden path", r.Component, ch.Path))
			}
		}
	}
	sort.Strings(probs)
	return probs
}

// Match reports whether file matches any glob. Globs use "/"-separated
// segments; "**" matches any number of segments, other segments use path.Match.
func Match(globs []string, file string) bool {
	for _, g := range globs {
		if matchSegs(strings.Split(g, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

func matchSegs(g, f []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			for i := 0; i <= len(f); i++ {
				if matchSegs(g[1:], f[i:]) {
					return true
				}
			}
			return false
		}
		if len(f) == 0 {
			return false
		}
		if ok, _ := path.Match(g[0], f[0]); !ok {
			return false
		}
		g, f = g[1:], f[1:]
	}
	return len(f) == 0
}

// GlobWithin reports whether every path matched by g is also matched by one of
// the ceiling globs. It is conservative: it checks g's literal prefix (the part
// before the first wildcard) against each ceiling. A nil ceiling means "never".
func GlobWithin(g string, ceiling []string) bool {
	prefix := literalPrefix(g)
	for _, c := range ceiling {
		if c == "**" {
			return true
		}
		cp := literalPrefix(c)
		if strings.HasSuffix(c, "/**") && (prefix == strings.TrimSuffix(cp, "/") || strings.HasPrefix(prefix, cp)) {
			return true
		}
		if g == c {
			return true
		}
	}
	return false
}

func literalPrefix(g string) string {
	if i := strings.IndexAny(g, "*?["); i >= 0 {
		return g[:i]
	}
	return g
}
