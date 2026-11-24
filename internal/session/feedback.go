package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/gh"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

// FeedbackRouter is H-WRK-07: CI failures and change requests on a
// session-owned change go to that session, as structured feedback.
type FeedbackRouter struct{ *Deps }

func (h FeedbackRouter) Name() string    { return "H-WRK-07.feedback-router" }
func (h FeedbackRouter) Types() []string { return []string{TRunCompleted, TReview} }

func (h FeedbackRouter) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	it, _, err := h.item(ctx, e.CorrelationID)
	if err != nil || it.Done {
		return nil, err
	}
	switch e.Type {
	case TRunCompleted:
		var r Run
		if err := e.Decode(&r); err != nil {
			return nil, err
		}
		m := it.Members[r.Component]
		if r.Conclusion == "success" || m == nil || m.Merged || r.HeadSHA != m.HeadSHA {
			return nil, nil
		}
		// Deterministic classification first (docs/12 §6): which checks and
		// steps failed, plus the log tail the agent needs.
		var steps []string
		var details strings.Builder
		checks, _ := gh.Checks(ctx, r.Repo, r.HeadSHA)
		for _, c := range checks {
			if c.Conclusion != "failure" || !contains(r.Failed, c.Name) {
				continue
			}
			s := gh.FailedSteps(ctx, r.Repo, c.ID)
			steps = append(steps, s...)
			fmt.Fprintf(&details, "Check %q failed at step(s) %s. Log tail:\n%s\n\n", c.Name, strings.Join(s, ", "), gh.FailedLog(ctx, r.Repo, c.ID, 120))
		}
		class := "code"
		for _, s := range steps {
			switch {
			case strings.Contains(s, "npm ci"), strings.Contains(s, "Install"):
				class = "dependency"
			case strings.Contains(s, "lint"):
				class = "lint"
			case strings.Contains(s, "typecheck"):
				class = "type"
			case strings.Contains(s, "test"):
				class = "test"
			case strings.Contains(s, "build"), strings.Contains(s, "Build"):
				class = "build"
			}
		}
		summary := fmt.Sprintf("CI failed on %s: %s", r.Component, strings.Join(r.Failed, ", "))
		if len(steps) > 0 {
			summary += " (" + strings.Join(steps, ", ") + ")"
		}
		diag, err1 := emitFrom(e, TDiagnosis, r.HeadSHA, Diagnosis{
			Component: r.Component, HeadSHA: r.HeadSHA, Class: class, Method: "rules",
			Summary: summary, FailedChecks: r.Failed, FailedSteps: steps,
		})
		fb, err2 := emitFrom(e, TFeedback, r.HeadSHA, Feedback{
			SessionID: sessionID(it.Work.Key, r.Component), Component: r.Component, Source: "pipeline",
			HeadSHA: r.HeadSHA, Summary: summary, Details: tail(details.String(), 12000), Trusted: true,
		})
		return []*event.Event{diag, fb}, firstErr(err1, err2)

	case TReview:
		var r Review
		if err := e.Decode(&r); err != nil {
			return nil, err
		}
		m := it.Members[r.Component]
		if m == nil || m.Merged || r.State != "changes_requested" || !contains(h.Cfg.Sessions.Reviewers, r.Reviewer) {
			return nil, nil
		}
		fb, err := emitFrom(e, TFeedback, fmt.Sprint(r.ReviewID), Feedback{
			SessionID: sessionID(it.Work.Key, r.Component), Component: r.Component, Source: "review by " + r.Reviewer,
			HeadSHA: m.HeadSHA, Summary: "Changes requested in review", Details: r.Body, Trusted: true,
		})
		return []*event.Event{fb}, err
	}
	return nil, nil
}

// Coordinator is H-WRK-09 (+ completion): marks the changeset ready when every
// member is green, records merges, and closes the work item once deployed.
// A failed deploy blocks the work item and escalates to the sponsor instead.
type Coordinator struct{ *Deps }

func (h Coordinator) Name() string    { return "H-WRK-09.changeset-coordinator" }
func (h Coordinator) Types() []string { return []string{TRunCompleted, TMerged, TDeployed} }

func (h Coordinator) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	evs, err := h.Store.Correlation(ctx, e.CorrelationID)
	if err != nil {
		return nil, err
	}
	var upTo []*event.Event
	for _, x := range evs {
		upTo = append(upTo, x)
		if x.ID == e.ID {
			break
		}
	}
	it, err := Fold(upTo)
	if err != nil || it.Done {
		return nil, err
	}
	switch e.Type {
	case TRunCompleted:
		if it.CSReady || len(it.Members) == 0 {
			return nil, nil
		}
		for _, c := range it.Changeset {
			m := it.Members[c]
			if m == nil || m.ChangeNumber == 0 || !(m.Green() || m.Merged) {
				return nil, nil
			}
		}
		var out []*event.Event
		var order []string
		for i, c := range it.Changeset {
			m := it.Members[c]
			order = append(order, fmt.Sprintf("%d. %s#%d", i+1, m.Repo, m.ChangeNumber))
		}
		for _, c := range it.Changeset {
			m := it.Members[c]
			if m.Ready || m.Merged {
				continue
			}
			if err := gh.MarkReady(ctx, m.Repo, m.ChangeNumber); err != nil {
				return nil, err
			}
			_ = gh.RequestReview(ctx, m.Repo, m.ChangeNumber, h.Cfg.Sessions.Reviewers...)
			msg := fmt.Sprintf("✅ **Ready for review.** Every check in the %s changeset is green.\n\nMerge order:\n%s\n\nConsumers are re-pinned to the producer's merge commit automatically, and CI re-runs before they are mergeable. Live view: https://rob.expona.ai/pipeline",
				it.Work.Key, strings.Join(order, "\n"))
			_ = gh.Comment(ctx, m.Repo, m.ChangeNumber, msg)
			r, err := emitFrom(e, TReady, c, Ready{SessionID: sessionID(it.Work.Key, c), Component: c, Repo: m.Repo, ChangeNumber: m.ChangeNumber, URL: m.ChangeURL})
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		cs, err := emitFrom(e, TCSReady, "ready", map[string]any{"changesetId": "cs-" + it.Work.Key, "members": it.Changeset})
		return append(out, cs), err

	case TMerged:
		for _, c := range it.Changeset {
			if m := it.Members[c]; m == nil || !m.Merged {
				return nil, nil
			}
		}
		cs, err := emitFrom(e, TCSMerged, "merged", map[string]any{"changesetId": "cs-" + it.Work.Key, "members": it.Changeset})
		if err != nil || h.anyDeploy(it) {
			return []*event.Event{cs}, err
		}
		done, err := emitFrom(e, TWorkCompleted, "completed", map[string]any{"outcome": "merged", "changes": changes(it)})
		return []*event.Event{cs, done}, err

	case TDeployed:
		var d Deployed
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		if d.Conclusion != "success" {
			summary := fmt.Sprintf("%s deploy to %s at %.12s concluded %q (%s). Fix or re-run the deploy; a successful run completes the work item.",
				d.Component, d.Environment, d.SHA, d.Conclusion, d.URL)
			esc, err1 := emitFrom(e, work.TAgentEscalated, "deploy-failed-"+e.ID, work.Escalated{Reason: "deploy-failed", Summary: summary})
			blk, err2 := emitFrom(e, work.TWorkBlocked, "deploy-failed-"+e.ID, work.Reason{Reason: "deploy-failed", Detail: summary})
			return []*event.Event{esc, blk}, firstErr(err1, err2)
		}
		done, err := emitFrom(e, TWorkCompleted, "completed", map[string]any{
			"outcome": "merged-and-deployed", "deployment": d, "changes": changes(it), "costUsd": it.Work.CostUSD,
		})
		return []*event.Event{done}, err
	}
	return nil, nil
}

func (h Coordinator) anyDeploy(it *Item) bool {
	for c := range it.Members {
		if h.Cfg.Components[c].Deploy != "" {
			return true
		}
	}
	return false
}

func changes(it *Item) []string {
	var out []string
	for _, c := range it.Changeset {
		if m := it.Members[c]; m != nil && m.ChangeNumber > 0 {
			out = append(out, fmt.Sprintf("%s#%d", m.Repo, m.ChangeNumber))
		}
	}
	return out
}

func emitFrom(cause *event.Event, typ, key string, data any) (*event.Event, error) {
	return event.New(event.Spec{
		Type: typ, Subject: cause.Subject, Source: "//ap/sessions", Cause: cause, Key: key, Data: data,
		Origin: "system", Actor: &event.Actor{Kind: "system", ID: "agentic-pipeline"},
	})
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
