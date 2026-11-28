package jira

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

// Watcher polls Jira for open issues assigned to the agent account. A new
// issue becomes ap.work.item.assigned; each comment on it becomes
// ap.work.item.commented. Delivery keys come from Jira IDs, so polling the
// same state again publishes nothing.
type Watcher struct {
	Client   *Client
	Store    *event.Store
	Cfg      *config.Config
	Log      *slog.Logger
	Interval time.Duration
}

func (w *Watcher) Run(ctx context.Context) {
	iv := w.Interval
	if iv == 0 {
		iv = time.Duration(w.Cfg.Jira.PollSeconds) * time.Second
	}
	if iv <= 0 {
		iv = 30 * time.Second
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if n, err := w.Once(ctx); err != nil {
			w.Log.Warn("jira poll", "err", err)
		} else if n > 0 {
			w.Log.Info("jira poll", "newEvents", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// JQL is the query for open issues assigned to the agent account.
func (w *Watcher) JQL() string {
	j := w.Cfg.Jira
	if j.JQL != "" {
		return j.JQL
	}
	q := fmt.Sprintf(`assignee = "%s" AND statusCategory != Done`, j.AgentAccountID)
	if j.TriggerLabel != "" {
		q = fmt.Sprintf(`labels = "%s" AND statusCategory != Done`, j.TriggerLabel)
	}
	if len(j.Projects) > 0 {
		q = fmt.Sprintf(`project in (%s) AND %s`, quoteAll(j.Projects), q)
	}
	return q + " ORDER BY created ASC"
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return strings.Join(q, ", ")
}

func (w *Watcher) fields() []string {
	f := []string{"summary", "issuetype", "priority", "components", "labels", "description", "reporter", "assignee"}
	if ac := w.Cfg.Jira.AcceptanceCriteriaField; ac != "" {
		f = append(f, ac)
	}
	return f
}

// Once polls Jira once and returns how many new events it published.
func (w *Watcher) Once(ctx context.Context) (int, error) {
	issues, err := w.Client.Search(ctx, w.JQL(), w.fields())
	if err != nil {
		return 0, err
	}
	total := 0
	for i := range issues {
		n, err := w.issue(ctx, &issues[i])
		total += n
		if err != nil {
			w.Log.Warn("jira issue", "key", issues[i].Key, "err", err)
		}
	}
	return total, nil
}

func (w *Watcher) issue(ctx context.Context, is *Issue) (int, error) {
	if !w.triggered(is) {
		return 0, nil // a custom JQL can return other issues
	}
	card := w.Card(is)
	provider := map[string]string{"name": "jira", "id": is.ID, "url": strings.TrimRight(w.Client.Site, "/") + "/browse/" + is.Key}
	n, err := work.SubmitIssue(ctx, w.Store, w.Cfg, card, provider)
	if err != nil {
		return n, err
	}
	comments, err := w.Client.Comments(ctx, is.Key)
	if err != nil {
		return n, err
	}
	for _, c := range comments {
		if c.Author != nil && w.Cfg.Jira.AgentAccountID != "" && c.Author.AccountID == w.Cfg.Jira.AgentAccountID {
			continue // the agent's own comments (bot loop, docs/05 §3)
		}
		body := Text(c.Body)
		if body == "" {
			continue
		}
		added, err := work.PublishComment(ctx, w.Store, w.Cfg, is.Key, "jira-"+c.ID, w.user(c.Author), body, provider["name"])
		if err != nil {
			return n, err
		}
		n += added
	}
	return n, nil
}

// triggered reports whether an issue is the agent's: it carries the trigger
// label, or (without one) it is assigned to the agent account.
func (w *Watcher) triggered(is *Issue) bool {
	if l := w.Cfg.Jira.TriggerLabel; l != "" {
		return slices.Contains(is.Fields.Labels, l)
	}
	return is.Fields.Assignee != nil && is.Fields.Assignee.AccountID == w.Cfg.Jira.AgentAccountID
}

// Card maps a Jira issue onto the tracker card the work plane uses.
func (w *Watcher) Card(is *Issue) *work.Card {
	f := is.Fields
	desc, ac := SplitCriteria(f.Description)
	if field := w.Cfg.Jira.AcceptanceCriteriaField; field != "" {
		ac = Criteria(f.Extra[field])
		desc = Text(f.Description)
	}
	c := &work.Card{
		Key: is.Key, IssueType: f.IssueType.Name, Summary: f.Summary,
		Assignee: w.Cfg.AgentUser, Sponsor: w.user(f.Reporter),
		Description: desc, AcceptanceCriteria: ac,
	}
	if f.Priority != nil {
		c.Priority = f.Priority.Name
	}
	// Components name the repos. Team-managed projects have no components, so
	// a label equal to a configured component name counts too.
	for _, comp := range f.Components {
		c.Components = append(c.Components, strings.ToLower(comp.Name))
	}
	for _, l := range f.Labels {
		l = strings.ToLower(l)
		if _, ok := w.Cfg.Components[l]; ok && !slices.Contains(c.Components, l) {
			c.Components = append(c.Components, l)
		}
	}
	return c
}

// user maps a Jira account to its tracker identity. Unmapped accounts keep a
// jira: name, which intake and the comment parser treat as unmapped.
func (w *Watcher) user(u *User) string {
	if u == nil {
		return "jira:unknown"
	}
	for name, id := range w.Cfg.Identities {
		if id.Jira != "" && id.Jira == u.AccountID {
			return name
		}
	}
	return "jira:" + u.DisplayName
}

// Preview prints what a poll would submit, without publishing anything.
func (w *Watcher) Preview(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "JQL: %s\n", w.JQL())
	issues, err := w.Client.Search(ctx, w.JQL(), w.fields())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d issue(s)\n", len(issues))
	for i := range issues {
		is := &issues[i]
		c := w.Card(is)
		fmt.Fprintf(out, "\n%s  %s  [%s]\n", c.Key, c.Summary, c.IssueType)
		fmt.Fprintf(out, "  sponsor: %s  components: %v  acceptance criteria: %d\n", c.Sponsor, c.Components, len(c.AcceptanceCriteria))
		for _, ac := range c.AcceptanceCriteria {
			fmt.Fprintf(out, "    - %s\n", ac)
		}
		if comments, err := w.Client.Comments(ctx, is.Key); err == nil {
			fmt.Fprintf(out, "  comments: %d\n", len(comments))
		}
		for _, p := range w.Problems(c) {
			fmt.Fprintf(out, "  WOULD BE REJECTED: %s\n", p)
		}
	}
	return nil
}

// Problems lists the intake checks (work.Orchestrator) a card would fail.
func (w *Watcher) Problems(c *work.Card) []string {
	var p []string
	if !w.Cfg.Eligible(c.IssueType) {
		p = append(p, "issue type "+c.IssueType+" is not in eligibleIssueTypes")
	}
	if !w.Cfg.IsIdentity(c.Sponsor) {
		p = append(p, "reporter "+c.Sponsor+" has no identities entry with a jira account ID")
	}
	if len(c.AcceptanceCriteria) == 0 {
		p = append(p, "no acceptance criteria")
	}
	if len(c.Components) == 0 {
		p = append(p, "no components")
	}
	for _, comp := range c.Components {
		if _, ok := w.Cfg.Components[comp]; !ok {
			p = append(p, "component "+comp+" is not configured")
		}
	}
	return p
}
