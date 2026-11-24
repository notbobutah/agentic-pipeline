package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/gh"
)

// Poller is the reconciler (docs/02 §2.1) standing in for GitHub webhooks:
// it turns the state of session-owned PRs (checks, reviews, merges) and the
// deploy workflow into events. Every event is keyed by what it observed, so
// polling the same state twice publishes nothing new.
type Poller struct {
	*Deps
	Interval time.Duration
}

func (p *Poller) Run(ctx context.Context) {
	if p.Interval == 0 {
		p.Interval = 30 * time.Second
	}
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		if err := p.Once(ctx); err != nil {
			p.Log.Warn("poll", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once polls every open work item once.
func (p *Poller) Once(ctx context.Context) error {
	corrs, err := p.Store.Correlations(ctx)
	if err != nil {
		return err
	}
	for corr := range corrs {
		if !strings.HasPrefix(corr, "wi-") {
			continue
		}
		it, _, err := p.item(ctx, corr)
		if err != nil || it.Done {
			continue
		}
		for _, c := range it.Changeset {
			m := it.Members[c]
			if m == nil || m.Opened == nil || m.Merged {
				continue
			}
			if err := p.pollChange(ctx, m); err != nil {
				p.Log.Warn("poll change", "component", c, "err", err)
			}
		}
		if it.CSMerged && !it.Deployed {
			p.pollDeploy(ctx, it)
		}
	}
	return nil
}

func (p *Poller) pollChange(ctx context.Context, m *Member) error {
	cc := p.Cfg.Components[m.Component]
	pr, err := gh.GetPR(ctx, m.Repo, m.ChangeNumber)
	if err != nil {
		return err
	}
	if pr.Merged {
		by := ""
		if pr.MergedBy != nil {
			by = pr.MergedBy.Login
		}
		_, err := p.publish(ctx, m.Opened, TMerged, fmt.Sprint(pr.Number), Merged{
			Component: m.Component, Repo: m.Repo, ChangeNumber: pr.Number, MergeSHA: pr.MergeCommitSHA, MergedBy: by, URL: pr.HTMLURL,
		})
		return err
	}
	sha := pr.Head.SHA
	checks, err := gh.Checks(ctx, m.Repo, sha)
	if err != nil {
		return err
	}
	byName := map[string]gh.CheckRun{}
	for _, c := range checks {
		if contains(cc.Checks, c.Name) {
			byName[c.Name] = c
		}
	}
	var failed []string
	complete := len(byName) == len(cc.Checks)
	for _, name := range cc.Checks {
		c, ok := byName[name]
		if !ok || c.Status != "completed" {
			complete = false
			continue
		}
		if _, err := p.publish(ctx, m.Opened, TJobCompleted, sha+"/"+name, Job{
			Component: m.Component, Repo: m.Repo, ChangeNumber: m.ChangeNumber, HeadSHA: sha,
			Check: name, Conclusion: c.Conclusion, URL: c.HTMLURL, JobID: c.ID,
		}); err != nil {
			return err
		}
		if c.Conclusion != "success" && c.Conclusion != "skipped" && c.Conclusion != "neutral" {
			failed = append(failed, name)
		}
	}
	if complete {
		concl := "success"
		if len(failed) > 0 {
			concl = "failure"
		}
		if _, err := p.publish(ctx, m.Opened, TRunCompleted, sha, Run{
			Component: m.Component, Repo: m.Repo, ChangeNumber: m.ChangeNumber, HeadSHA: sha,
			Conclusion: concl, Checks: cc.Checks, Failed: failed,
		}); err != nil {
			return err
		}
	}
	return p.pollReviews(ctx, m)
}

func (p *Poller) pollReviews(ctx context.Context, m *Member) error {
	var reviews []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
		Body  string `json:"body"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := gh.API(ctx, &reviews, fmt.Sprintf("repos/%s/pulls/%d/reviews", m.Repo, m.ChangeNumber)); err != nil {
		return err
	}
	for _, r := range reviews {
		state := strings.ToLower(r.State)
		if state == "commented" && strings.TrimSpace(r.Body) == "" {
			continue
		}
		if _, err := p.publish(ctx, m.Opened, TReview, fmt.Sprint(r.ID), Review{
			Component: m.Component, Repo: m.Repo, ChangeNumber: m.ChangeNumber,
			Reviewer: r.User.Login, State: state, Body: r.Body, ReviewID: r.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) pollDeploy(ctx context.Context, it *Item) {
	for _, c := range it.Changeset {
		m := it.Members[c]
		wf := p.Cfg.Components[c].Deploy
		if m == nil || wf == "" || m.MergeSHA == "" {
			continue
		}
		runs, err := gh.Runs(ctx, m.Repo, wf)
		if err != nil {
			p.Log.Warn("poll deploy", "err", err)
			continue
		}
		for _, r := range runs {
			if r.HeadSHA != m.MergeSHA || r.Status != "completed" || r.Conclusion == "skipped" {
				continue
			}
			cause := m.Opened
			// A re-run keeps the run ID, so later attempts get their own key.
			key := fmt.Sprint(r.ID)
			if r.RunAttempt > 1 {
				key = fmt.Sprintf("%d-%d", r.ID, r.RunAttempt)
			}
			if _, err := p.publish(ctx, cause, TDeployed, key, Deployed{
				Component: c, Repo: m.Repo, SHA: r.HeadSHA, Environment: "dev", Conclusion: r.Conclusion, URL: r.HTMLURL, Attempt: r.RunAttempt,
			}); err != nil {
				p.Log.Warn("publish deploy", "err", err)
			}
			break
		}
	}
}

var _ = event.New
