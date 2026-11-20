package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/event"
)

// RenderPlan formats a plan version for the developer to review.
func RenderPlan(st *State, p *PlanProposed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implementation plan %s v%d  (%s)\n", p.PlanID, p.Version, p.Digest[:19])
	fmt.Fprintf(&b, "Work item %s: %s\n\n", st.Key, st.Card.Summary)
	fmt.Fprintf(&b, "Summary\n  %s\n\nApproach\n", p.Plan.Summary)
	for i, a := range p.Plan.Approach {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, a)
	}
	for _, r := range p.Plan.Repos {
		fmt.Fprintf(&b, "\nChanges in %s (%s)\n", r.Repo, r.Component)
		for _, c := range r.Changes {
			fmt.Fprintf(&b, "  %-7s %s\n          %s\n", c.Action, c.Path, c.Purpose)
		}
	}
	b.WriteString("\nTests\n")
	for _, t := range p.Plan.Tests {
		fmt.Fprintf(&b, "  [%s] %-12s %s  → %s\n", t.Component, t.Level, t.Name, strings.Join(t.Covers, ", "))
	}
	b.WriteString("\nScope envelope (agents may write only here)\n")
	for _, e := range p.Plan.ScopeEnvelope {
		fmt.Fprintf(&b, "  %-5s %s\n", e.Component, strings.Join(e.PathGlobs, "  "))
	}
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		for _, it := range items {
			fmt.Fprintf(&b, "  - %s\n", it)
		}
	}
	list("Out of scope", p.Plan.OutOfScope)
	list("Risks", p.Plan.Risks)
	list("OPEN QUESTIONS (block approval)", p.Plan.OpenQuestions)
	fmt.Fprintf(&b, "\nEstimate: %s iterations, ~$%.2f, ~%d min   (planning cost $%.2f)\n",
		p.Plan.Estimate.Iterations, p.Plan.Estimate.CostUSD, p.Plan.Estimate.WallclockMinutes, p.CostUSD)
	return b.String()
}

// DiffPlans gives a line diff between two rendered plan versions.
func DiffPlans(st *State, a, b *PlanProposed) string {
	al := strings.Split(RenderPlan(st, a), "\n")
	bl := strings.Split(RenderPlan(st, b), "\n")
	inA := map[string]int{}
	for _, l := range al {
		inA[l]++
	}
	inB := map[string]int{}
	for _, l := range bl {
		inB[l]++
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- v%d\n+++ v%d\n", a.Version, b.Version)
	for _, l := range al {
		if inB[l] == 0 && strings.TrimSpace(l) != "" {
			out.WriteString("- " + l + "\n")
		}
	}
	for _, l := range bl {
		if inA[l] == 0 && strings.TrimSpace(l) != "" {
			out.WriteString("+ " + l + "\n")
		}
	}
	return out.String()
}

// Reporter is H-WRK-10, the Work Reporter. Instead of Jira comments it
// renders the work item's activity feed to <cards>/<KEY>.activity.md. It
// re-renders from the full event history every time, so it is idempotent.
type Reporter struct {
	*Deps
	Dir string
}

func (h Reporter) Name() string { return "H-WRK-10.work-reporter" }
func (h Reporter) Types() []string {
	return []string{TRequestAccepted, TRequestRejected, TPlanProposed, TPlanApproved, TCommandRejected,
		TWorkBlocked, TWorkCancelled, TAgentEscalated, TSessionRequested}
}

func (h Reporter) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	evs, err := h.Store.Correlation(ctx, e.CorrelationID)
	if err != nil {
		return nil, err
	}
	st, err := Replay(evs)
	if err != nil {
		return nil, err
	}
	if st.Key == "" {
		return nil, nil
	}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(filepath.Join(h.Dir, st.Key+".activity.md"), []byte(Activity(st)), 0o644)
}

// Activity renders the tracker-comment view of a work item.
func Activity(st *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\nStatus: **%s** · agent cost so far: $%.2f\n\n", st.Key, st.Card.Summary, st.Status, st.CostUSD)
	for _, e := range st.Events {
		ts := e.Time.Format("15:04:05")
		var line string
		switch e.Type {
		case TRequestAccepted:
			line = "🤖 Picked up. Planning the implementation."
		case TRequestRejected:
			var d Reason
			_ = e.Decode(&d)
			line = fmt.Sprintf("🤖 Can't take this: %s (%s). Reassigning to the sponsor.", d.Reason, d.Detail)
		case TPlanProposed:
			var d PlanProposed
			_ = e.Decode(&d)
			line = fmt.Sprintf("🤖 @%s implementation plan **v%d** is ready for review (`ap plan show %s`). Reply `/ap approve`, or `/ap revise <feedback>`.", st.Card.Sponsor, d.Version, st.Key)
			if n := len(d.Plan.OpenQuestions); n > 0 {
				line += fmt.Sprintf(" It has **%d open question(s)**, which must be answered before approval.", n)
			}
		case TPlanApproved:
			var d PlanApproved
			_ = e.Decode(&d)
			line = fmt.Sprintf("✅ Plan v%d approved by @%s (digest `%s`). Starting coding sessions.", d.Version, d.Approver, d.Digest[:19])
		case TSessionRequested:
			var d SessionRequested
			_ = e.Decode(&d)
			line = fmt.Sprintf("🤖 Coding session requested for **%s** (%s), limited to %v.", d.Component, d.Repo, d.PathGlobs)
		case TCommandRejected:
			var d CommandOutcome
			_ = e.Decode(&d)
			line = fmt.Sprintf("⚠️ `/ap %s` rejected: %s", d.Command, d.Reason)
		case TAgentEscalated:
			var d Escalated
			_ = e.Decode(&d)
			line = fmt.Sprintf("🚩 @%s needs your input: %s. %s", st.Card.Sponsor, d.Reason, d.Summary)
		case TWorkCancelled:
			var d Reason
			_ = e.Decode(&d)
			line = "⏹️ Cancelled: " + d.Reason
		case TItemCommented:
			var d Commented
			_ = e.Decode(&d)
			line = fmt.Sprintf("💬 **%s**: %s", d.Author, d.Body)
		default:
			continue
		}
		fmt.Fprintf(&b, "- `%s` %s\n", ts, line)
	}
	return b.String()
}
