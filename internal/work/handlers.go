package work

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/agent"
	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
)

// Deps are shared by the work-plane handlers.
type Deps struct {
	Store *event.Store
	Cfg   *config.Config
	Agent agent.Runner
	Log   *slog.Logger
}

func (d *Deps) state(ctx context.Context, e *event.Event) (*State, error) {
	evs, err := d.Store.Correlation(ctx, e.CorrelationID)
	if err != nil {
		return nil, err
	}
	// Only events up to and including e: a handler decides on what was known
	// when e happened, so replays give the same answer.
	var upTo []*event.Event
	for _, x := range evs {
		upTo = append(upTo, x)
		if x.ID == e.ID {
			break
		}
	}
	return Replay(upTo)
}

func emit(cause *event.Event, typ, key string, data any) (*event.Event, error) {
	return event.New(event.Spec{
		Type: typ, Subject: cause.Subject, Source: "//ap/work", Cause: cause, Key: key, Data: data,
		Origin: "system", Actor: &event.Actor{Kind: "system", ID: "agentic-pipeline"},
	})
}

func collect(evs ...*event.Event) []*event.Event { return evs }

// ---------------------------------------------------------------------------
// H-WRK-01 Work Intake Router: assignment to the agent user → work request.

type IntakeRouter struct{ *Deps }

func (h IntakeRouter) Name() string    { return "H-WRK-01.work-intake-router" }
func (h IntakeRouter) Types() []string { return []string{TItemAssigned} }

func (h IntakeRouter) Handle(_ context.Context, e *event.Event) ([]*event.Event, error) {
	var d Assigned
	if err := e.Decode(&d); err != nil {
		return nil, err
	}
	if !d.ToAgent {
		return nil, nil
	}
	out, err := emit(e, TRequestReceived, "intake", RequestReceived{Key: d.Key, Trigger: "assignment", Sponsor: d.Sponsor})
	return collect(out), err
}

// ---------------------------------------------------------------------------
// H-WRK-02 Work Comment Parser: tracker comments → guidance, revisions, commands.
// Only mapped identities (the sponsor and team) count as instruction sources;
// anyone else's comment is context, never instruction (docs/14 §4.3).

type CommentParser struct{ *Deps }

func (h CommentParser) Name() string    { return "H-WRK-02.work-comment-parser" }
func (h CommentParser) Types() []string { return []string{TItemCommented} }

func (h CommentParser) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	var c Commented
	if err := e.Decode(&c); err != nil {
		return nil, err
	}
	if !h.Cfg.IsIdentity(c.Author) {
		h.Log.Info("comment from unmapped user treated as context only", "author", c.Author, "key", c.Key)
		return nil, nil
	}
	body := strings.TrimSpace(c.Body)
	if !strings.HasPrefix(body, "/ap ") && body != "/ap" {
		st, err := h.state(ctx, e)
		if err != nil {
			return nil, err
		}
		role := "team-member"
		if c.Author == st.Card.Sponsor {
			role = "sponsor"
		}
		g, err := emit(e, TGuidance, c.CommentID, Guidance{
			WorkItemID: e.CorrelationID, Author: c.Author, AuthorRole: role, Channel: "tracker-comment", Content: body, Scope: "all",
		})
		return collect(g), err
	}
	verb, args, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(body, "/ap")), " ")
	args = strings.TrimSpace(args)
	switch verb {
	case "revise":
		st, err := h.state(ctx, e)
		if err != nil {
			return nil, err
		}
		latest := st.Latest()
		if latest == nil || st.Status != StPlanReview {
			r, err := emit(e, TCommandRejected, c.CommentID, CommandOutcome{CommandID: c.CommentID, Command: "revise", Reason: "no plan awaiting review"})
			return collect(r), err
		}
		if args == "" {
			r, err := emit(e, TCommandRejected, c.CommentID, CommandOutcome{CommandID: c.CommentID, Command: "revise", Reason: "revise needs feedback text"})
			return collect(r), err
		}
		r, err := emit(e, TPlanRevision, c.CommentID, PlanRevision{Version: latest.Version, RequestedBy: c.Author, Feedback: args})
		return collect(r), err
	default:
		cmd, err := emit(e, TCommandReceived, c.CommentID, Command{CommandID: c.CommentID, Command: verb, Args: args, Issuer: c.Author, Channel: "tracker-comment"})
		return collect(cmd), err
	}
}

// ---------------------------------------------------------------------------
// H-WRK-03 Work Item Orchestrator (+ H-WRK-04 Repo Resolver, deterministic).

type Orchestrator struct{ *Deps }

func (h Orchestrator) Name() string { return "H-WRK-03.work-item-orchestrator" }
func (h Orchestrator) Types() []string {
	return []string{TRequestReceived, TPlanRevision, TPlanApproved, TCommandReceived}
}

func (h Orchestrator) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	st, err := h.state(ctx, e)
	if err != nil {
		return nil, err
	}
	switch e.Type {
	case TRequestReceived:
		return h.intake(e, st)
	case TPlanRevision:
		var d PlanRevision
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		next := st.Requested + 1
		r, err := emit(e, TPlanRequested, fmt.Sprint(next), PlanRequested{WorkItemID: e.CorrelationID, Version: next, Feedback: d.Feedback})
		return collect(r), err
	case TPlanApproved:
		return h.startSessions(e, st)
	case TCommandReceived:
		var c Command
		if err := e.Decode(&c); err != nil {
			return nil, err
		}
		if c.Command != "cancel" {
			return nil, nil // other commands belong to other handlers
		}
		if st.Status == StCancelled {
			return nil, nil
		}
		a, err1 := emit(e, TCommandAccepted, "accepted", CommandOutcome{CommandID: c.CommandID, Command: c.Command})
		x, err2 := emit(e, TWorkCancelled, "cancel", Reason{Reason: "manual", Detail: "cancelled by " + c.Issuer})
		return collect(a, x), firstErr(err1, err2)
	}
	return nil, nil
}

func (h Orchestrator) intake(e *event.Event, st *State) ([]*event.Event, error) {
	reject := func(reason, detail string) ([]*event.Event, error) {
		r, err := emit(e, TRequestRejected, "eligibility", Reason{Reason: reason, Detail: detail})
		return collect(r), err
	}
	c := st.Card
	if !h.Cfg.Eligible(c.IssueType) {
		return reject("issue-type", c.IssueType+" is not eligible")
	}
	if !h.Cfg.IsIdentity(c.Sponsor) {
		return reject("no-identity-mapping", "sponsor "+c.Sponsor+" is not mapped to a GitHub identity")
	}
	if len(c.AcceptanceCriteria) == 0 {
		return reject("no-acceptance-criteria", "the card has no acceptance criteria")
	}
	var repos []ResolvedRepo
	for _, comp := range c.Components {
		cc, ok := h.Cfg.Components[comp]
		if !ok {
			return reject("no-repo", "component "+comp+" has no repo mapping")
		}
		repos = append(repos, ResolvedRepo{Component: comp, Repo: cc.Repo, Confidence: 1, Source: "component-map"})
	}
	if len(repos) == 0 {
		return reject("no-repo", "the card names no components")
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Repo)
	}
	acc, err1 := emit(e, TRequestAccepted, "accepted", map[string]any{
		"workItemId": e.CorrelationID, "sponsor": c.Sponsor, "team": h.Cfg.Team, "candidateRepos": names,
	})
	res, err2 := emit(e, TReposResolved, "repos", ReposResolved{Repos: repos})
	req, err3 := emit(e, TPlanRequested, "1", PlanRequested{WorkItemID: e.CorrelationID, Version: 1})
	return collect(acc, res, req), firstErr(err1, err2, err3)
}

// startSessions requests one coding session per changed component (docs/14 §6)
// and groups them into a changeset when more than one repo is involved.
func (h Orchestrator) startSessions(e *event.Event, st *State) ([]*event.Event, error) {
	var a PlanApproved
	if err := e.Decode(&a); err != nil {
		return nil, err
	}
	p := st.Plan(a.Version)
	if p == nil {
		return nil, fmt.Errorf("approved plan v%d not found", a.Version)
	}
	env := map[string][]string{}
	for _, se := range p.Plan.ScopeEnvelope {
		env[se.Component] = se.PathGlobs
	}
	var out []*event.Event
	var members []string
	for _, r := range p.Plan.Repos {
		s, err := emit(e, TSessionRequested, r.Component, SessionRequested{
			WorkItemID: e.CorrelationID, PlanID: a.PlanID, Version: a.Version,
			Component: r.Component, Repo: r.Repo, PathGlobs: env[r.Component],
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		members = append(members, r.Component)
	}
	if len(members) > 1 {
		var order []string
		for _, c := range h.Cfg.DependencyOrder {
			for _, m := range members {
				if m == c {
					order = append(order, c)
				}
			}
		}
		cs, err := emit(e, TChangesetCreated, "changeset", ChangesetCreated{
			ChangesetID: "cs-" + st.Key, WorkItemID: e.CorrelationID, Members: members, MergeOrder: order,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// H-WRK-05 Plan Approval Manager (ADR-0007).

type PlanApproval struct{ *Deps }

func (h PlanApproval) Name() string    { return "H-WRK-05.plan-approval-manager" }
func (h PlanApproval) Types() []string { return []string{TCommandReceived} }

func (h PlanApproval) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	var c Command
	if err := e.Decode(&c); err != nil {
		return nil, err
	}
	if c.Command != "approve" && c.Command != "reject" {
		return nil, nil
	}
	st, err := h.state(ctx, e)
	if err != nil {
		return nil, err
	}
	reject := func(reason string) ([]*event.Event, error) {
		pd, err1 := emit(e, TPolicyDecision, "policy", PolicyDecision{Query: "plan." + c.Command, Result: "deny", Reasons: []string{reason}})
		r, err2 := emit(e, TCommandRejected, "rejected", CommandOutcome{CommandID: c.CommandID, Command: c.Command, Reason: reason})
		return collect(pd, r), firstErr(err1, err2)
	}
	latest := st.Latest()
	switch {
	case st.Status != StPlanReview || latest == nil:
		return reject(fmt.Sprintf("no plan awaiting review (status %s)", st.Status))
	case c.Issuer == h.Cfg.AgentUser:
		return reject("the agent identity can never approve")
	case c.Issuer != st.Card.Sponsor:
		return reject("only the sponsoring developer (" + st.Card.Sponsor + ") may approve or reject")
	}
	// Optional explicit version: "/ap approve v2" must match the latest.
	if v := strings.TrimPrefix(strings.TrimSpace(c.Args), "v"); v != "" && v != fmt.Sprint(latest.Version) {
		return reject(fmt.Sprintf("v%s is not the plan under review (v%d)", v, latest.Version))
	}
	if c.Command == "reject" {
		pd, err1 := emit(e, TPolicyDecision, "policy", PolicyDecision{Query: "plan.reject", Result: "allow"})
		a, err2 := emit(e, TCommandAccepted, "accepted", CommandOutcome{CommandID: c.CommandID, Command: c.Command})
		r, err3 := emit(e, TPlanRejected, "rejected", map[string]any{"planId": latest.PlanID, "version": latest.Version, "rejectedBy": c.Issuer})
		x, err4 := emit(e, TWorkCancelled, "cancel", Reason{Reason: "plan-rejected"})
		return collect(pd, a, r, x), firstErr(err1, err2, err3, err4)
	}
	if n := len(latest.Plan.OpenQuestions); n > 0 {
		return reject(fmt.Sprintf("plan v%d has %d open question(s); answer them in a comment and /ap revise", latest.Version, n))
	}
	if probs := ValidatePlan(&latest.Plan, &st.Card, h.Cfg); len(probs) > 0 {
		return reject("plan fails validation: " + strings.Join(probs, "; "))
	}
	pd, err1 := emit(e, TPolicyDecision, "policy", PolicyDecision{Query: "plan.approve", Result: "allow",
		Reasons: []string{"issuer is sponsor", "no open questions", "plan validates", "digest " + latest.Digest}})
	a, err2 := emit(e, TCommandAccepted, "accepted", CommandOutcome{CommandID: c.CommandID, Command: c.Command})
	ap, err3 := emit(e, TPlanApproved, "approved", PlanApproved{
		PlanID: latest.PlanID, Version: latest.Version, Digest: latest.Digest, Approver: c.Issuer, Channel: c.Channel,
	})
	return collect(pd, a, ap), firstErr(err1, err2, err3)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
