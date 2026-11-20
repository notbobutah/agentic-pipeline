package work

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/notbobutah/agentic-pipeline/internal/agent"
	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	return filepath.Join(dir, "..", "..")
}

func setup(t *testing.T) (*config.Config, *Card, *Plan) {
	t.Helper()
	root := repoRoot(t)
	cfg, err := config.Load(filepath.Join(root, "demo/config.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	card, err := LoadCard(filepath.Join(root, "demo/cards/DEMO-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "demo/fixtures/implementation-planner.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return cfg, card, &p
}

func TestFixturePlanValidates(t *testing.T) {
	cfg, card, p := setup(t)
	if probs := ValidatePlan(p, card, cfg); len(probs) > 0 {
		t.Fatalf("fixture plan should validate, got: %v", probs)
	}
}

func TestValidatePlanCatchesProblems(t *testing.T) {
	cfg, card, p := setup(t)
	p.Tests = p.Tests[:1] // drops coverage for AC3..AC5
	p.ScopeEnvelope[1].PathGlobs = append(p.ScopeEnvelope[1].PathGlobs, "server/**")
	p.Repos[1].Changes = append(p.Repos[1].Changes, FileChange{Path: "web/src/app/page.tsx", Action: "modify", Purpose: "x"})
	p.ScopeEnvelope[0].PathGlobs = append(p.ScopeEnvelope[0].PathGlobs, ".github/workflows/**")
	got := strings.Join(ValidatePlan(p, card, cfg), "\n")
	for _, want := range []string{
		"AC3 is not covered", "AC5 is not covered",
		`web glob "server/**" exceeds allowed`,
		`"web/src/app/page.tsx" is outside its scope envelope`,
		`data glob ".github/workflows/**" is inside forbidden ".github/**"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing problem %q in:\n%s", want, got)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		glob, file string
		want       bool
	}{
		{"web/**", "web/src/app/catalog/page.tsx", true},
		{"web/src/app/catalog/**", "web/src/app/page.tsx", false},
		{"**", "anything/at/all.json", true},
		{"index.d.ts", "index.d.ts", true},
		{"data/*.json", "data/products.json", true},
		{"data/*.json", "data/x/products.json", false},
	}
	for _, c := range cases {
		if got := Match([]string{c.glob}, c.file); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.glob, c.file, got, c.want)
		}
	}
}

// TestFlow drives the handlers end to end with the fixture agent.
func TestFlow(t *testing.T) {
	ctx := context.Background()
	cfg, card, _ := setup(t)
	cfg.StateDir = t.TempDir()
	cat, err := event.LoadCatalog(filepath.Join(repoRoot(t), "demo/catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := event.Open(filepath.Join(cfg.StateDir, "events.db"), cat)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := &Deps{Store: st, Cfg: cfg, Log: log, Agent: &agent.Fake{Dir: filepath.Join(repoRoot(t), "demo/fixtures")}}
	d := &event.Dispatcher{Store: st, Log: log, Handlers: []event.Handler{
		IntakeRouter{deps}, CommentParser{deps}, Orchestrator{deps}, PlanApproval{deps}, Planner{deps},
	}}
	drain := func() {
		for {
			p, err := d.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !p {
				return
			}
		}
	}
	state := func() *State {
		s, err := Load(ctx, st, cfg.Tenant, card.Key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	if _, err := SubmitCard(ctx, st, cfg, card); err != nil {
		t.Fatal(err)
	}
	drain()
	if s := state(); s.Status != StPlanReview || len(s.Plans) != 1 || len(s.Guidance) != 3 {
		t.Fatalf("after submit: status=%s plans=%d guidance=%d", s.Status, len(s.Plans), len(s.Guidance))
	}

	// An unmapped user cannot approve; a mapped non-sponsor command is rejected by policy.
	_ = Comment(ctx, st, cfg, card.Key, "mallory", "/ap approve")
	drain()
	if s := state(); s.Approved != nil {
		t.Fatal("unmapped user approved the plan")
	}

	_ = Comment(ctx, st, cfg, card.Key, "rob", "/ap revise group by category")
	drain()
	_ = Comment(ctx, st, cfg, card.Key, "rob", "/ap approve v1") // stale
	drain()
	if s := state(); s.Approved != nil || len(s.Plans) != 2 {
		t.Fatalf("stale approval accepted or revision missing: approved=%v plans=%d", s.Approved, len(s.Plans))
	}
	_ = Comment(ctx, st, cfg, card.Key, "rob", "/ap approve")
	drain()
	s := state()
	if s.Status != StImplementing || s.Approved == nil || s.Approved.Version != 2 || s.Approved.Digest != s.Plan(2).Digest {
		t.Fatalf("approval: status=%s approved=%+v", s.Status, s.Approved)
	}
	var sessions, changesets int
	for _, e := range s.Events {
		switch e.Type {
		case TSessionRequested:
			sessions++
		case TChangesetCreated:
			changesets++
		}
	}
	if sessions != 2 || changesets != 1 {
		t.Fatalf("want 2 sessions + 1 changeset, got %d + %d", sessions, changesets)
	}

	// Replaying the whole log through fresh handler offsets adds nothing.
	n := len(s.Events)
	for _, h := range d.Handlers {
		_ = st.Commit(ctx, h.Name(), 0)
	}
	drain()
	if m := len(state().Events); m != n {
		t.Fatalf("replay was not idempotent: %d → %d events", n, m)
	}
}
