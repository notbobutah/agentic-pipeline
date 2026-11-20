// Command ap is the agentic-pipeline demo: an event log, the work-plane
// handlers and a CLI that stands in for the tracker (Jira) and the console.
//
//	ap serve [--once]                 run the dispatchers
//	ap card submit <card.json>        assign a card to the agent
//	ap card comment <KEY> <text>      comment as a tracker user (guidance or /ap command)
//	ap plan show <KEY> [-v N] [--diff]
//	ap approve <KEY> | ap revise <KEY> <feedback> | ap reject <KEY> | ap cancel <KEY>
//	ap status <KEY> | ap timeline <KEY> | ap events [-n N]
//	ap feed build [--out DIR] | ap feed publish   public JSON snapshots for /pipeline
//
// Environment: AP_CONFIG (demo/config.yaml), AP_CATALOG (demo/catalog.yaml),
// AP_USER (tracker identity; default $USER), AP_FAKE_AGENT=1 (fixture agent),
// AP_AGENT_AUTH (api-key | claude-login; overrides agents.auth).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/agent"
	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/feed"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ap:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type app struct {
	cfg   *config.Config
	store *event.Store
	log   *slog.Logger
	user  string
}

func open() (*app, error) {
	base, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(env("AP_CONFIG", "demo/config.yaml"), base)
	if err != nil {
		return nil, err
	}
	cat, err := event.LoadCatalog(cfg.Path(env("AP_CATALOG", "demo/catalog.yaml")))
	if err != nil {
		return nil, err
	}
	event.Tenant, event.Team = cfg.Tenant, cfg.Team
	if err := os.MkdirAll(cfg.StatePath(), 0o755); err != nil {
		return nil, err
	}
	st, err := event.Open(cfg.StatePath("events.db"), cat)
	if err != nil {
		return nil, err
	}
	level := slog.LevelInfo
	if os.Getenv("AP_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return &app{
		cfg: cfg, store: st,
		log:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})),
		user: env("AP_USER", env("USER", "unknown")),
	}, nil
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ap <serve|card|plan|approve|revise|reject|cancel|status|timeline|events|feed> ...")
	}
	a, err := open()
	if err != nil {
		return err
	}
	defer a.store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := args[0], args[1:]
	need := func(n int, usage string) error {
		if len(rest) < n {
			return errors.New("usage: ap " + usage)
		}
		return nil
	}
	switch cmd {
	case "serve":
		return a.serve(ctx, rest)
	case "card":
		if err := need(2, "card submit <file> | card comment <KEY> <text>"); err != nil {
			return err
		}
		switch rest[0] {
		case "submit":
			c, err := work.LoadCard(rest[1])
			if err != nil {
				return err
			}
			n, err := work.SubmitCard(ctx, a.store, a.cfg, c)
			if err != nil {
				return err
			}
			fmt.Printf("%s submitted (%d new events). Correlation %s\n", c.Key, n, work.CorrelationID(a.cfg.Tenant, c.Key))
			return nil
		case "comment":
			if err := need(3, "card comment <KEY> <text>"); err != nil {
				return err
			}
			return a.comment(ctx, rest[1], strings.Join(rest[2:], " "))
		}
		return fmt.Errorf("unknown card subcommand %q", rest[0])
	case "approve", "reject", "cancel":
		if err := need(1, cmd+" <KEY> [vN]"); err != nil {
			return err
		}
		return a.comment(ctx, rest[0], strings.TrimSpace("/ap "+cmd+" "+strings.Join(rest[1:], " ")))
	case "revise":
		if err := need(2, "revise <KEY> <feedback>"); err != nil {
			return err
		}
		return a.comment(ctx, rest[0], "/ap revise "+strings.Join(rest[1:], " "))
	case "plan":
		return a.planShow(ctx, rest)
	case "status":
		if err := need(1, "status <KEY>"); err != nil {
			return err
		}
		return a.status(ctx, rest[0])
	case "timeline":
		if err := need(1, "timeline <KEY>"); err != nil {
			return err
		}
		return a.timeline(ctx, rest[0])
	case "feed":
		return a.feedCmd(ctx, rest)
	case "events":
		fs := flag.NewFlagSet("events", flag.ContinueOnError)
		n := fs.Int("n", 30, "last N events")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return a.events(ctx, *n)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func (a *app) comment(ctx context.Context, key, body string) error {
	if err := work.Comment(ctx, a.store, a.cfg, key, a.user, body); err != nil {
		return err
	}
	fmt.Printf("comment on %s as %s: %s\n", key, a.user, body)
	return nil
}

func (a *app) runner() agent.Runner {
	if os.Getenv("AP_FAKE_AGENT") != "" {
		a.log.Info("using fixture agent (AP_FAKE_AGENT)")
		return &agent.Fake{Dir: a.cfg.Path("demo/fixtures"), Log: a.log}
	}
	return &agent.Docker{Image: a.cfg.Agents.Image, Auth: env("AP_AGENT_AUTH", a.cfg.Agents.Auth), StateDir: a.cfg.StatePath(), Log: a.log}
}

// dispatchers builds the handler groups: fast routing/state handlers, and the
// agent invokers (slow) in their own group so they never block routing.
func (a *app) dispatchers(r agent.Runner) (fast, agents *event.Dispatcher) {
	deps := &work.Deps{Store: a.store, Cfg: a.cfg, Agent: r, Log: a.log}
	fast = &event.Dispatcher{Store: a.store, Log: a.log, Handlers: []event.Handler{
		work.IntakeRouter{Deps: deps},
		work.CommentParser{Deps: deps},
		work.Orchestrator{Deps: deps},
		work.PlanApproval{Deps: deps},
		work.Reporter{Deps: deps, Dir: a.cfg.Path("demo/cards")},
	}}
	agents = &event.Dispatcher{Store: a.store, Log: a.log, Handlers: []event.Handler{
		work.Planner{Deps: deps},
	}}
	return fast, agents
}

func (a *app) pipelineInfo(groups ...*event.Dispatcher) feed.Pipeline {
	p := feed.Pipeline{Name: "agentic-pipeline", Repo: "https://github.com/notbobutah/agentic-pipeline"}
	if cat, err := event.LoadCatalog(a.cfg.Path(env("AP_CATALOG", "demo/catalog.yaml"))); err == nil {
		p.CatalogTypes = cat.Len()
	}
	for _, g := range groups {
		for _, h := range g.Handlers {
			id, name, _ := strings.Cut(h.Name(), ".")
			p.Handlers = append(p.Handlers, feed.HandlerInfo{ID: id, Name: name})
		}
	}
	names := make([]string, 0, len(a.cfg.Components))
	for n := range a.cfg.Components {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := a.cfg.Components[n]
		p.Components = append(p.Components, feed.Component{Name: n, Repo: c.Repo, Root: c.Root})
	}
	return p
}

func (a *app) publisher() *feed.Publisher {
	return &feed.Publisher{
		Dir:    a.cfg.StatePath("feed"),
		Remote: "https://github.com/" + a.cfg.Feed.Repo + ".git",
		Branch: a.cfg.Feed.Branch,
	}
}

// publishFeed builds the snapshot into the feed worktree and pushes it.
func (a *app) publishFeed(ctx context.Context, info feed.Pipeline) error {
	if a.cfg.Feed.Repo == "" || a.cfg.Feed.Branch == "" {
		return errors.New("feed.repo and feed.branch must be set in the config")
	}
	pub := a.publisher()
	if err := pub.Init(ctx); err != nil {
		return err
	}
	idx, err := feed.Build(ctx, a.store, a.cfg, info, pub.Dir)
	if err != nil {
		return err
	}
	if err := pub.Publish(ctx); err != nil {
		return err
	}
	a.log.Info("feed published", "cards", len(idx.Cards), "url", a.cfg.Feed.URL())
	return nil
}

func (a *app) feedCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ap feed build [--out DIR] | ap feed publish")
	}
	fast, agents := a.dispatchers(nil)
	info := a.pipelineInfo(fast, agents)
	switch args[0] {
	case "build":
		fs := flag.NewFlagSet("feed build", flag.ContinueOnError)
		out := fs.String("out", a.cfg.StatePath("feed-local"), "output directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		idx, err := feed.Build(ctx, a.store, a.cfg, info, *out)
		if err != nil {
			return err
		}
		fmt.Printf("feed: %d card(s) → %s\n", len(idx.Cards), *out)
		return nil
	case "publish":
		return a.publishFeed(ctx, info)
	}
	return fmt.Errorf("unknown feed subcommand %q", args[0])
}

func (a *app) serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	once := fs.Bool("once", false, "process until idle, then exit")
	publish := fs.Bool("publish-feed", false, "publish the public feed when the log changes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fast, agents := a.dispatchers(a.runner())
	info := a.pipelineInfo(fast, agents)
	if *once {
		for {
			p1, err := fast.Step(ctx)
			if err != nil {
				return err
			}
			p2, err := agents.Step(ctx)
			if err != nil {
				return err
			}
			if !p1 && !p2 {
				break
			}
		}
		if *publish {
			return a.publishFeed(ctx, info)
		}
		return nil
	}
	a.log.Info("serving", "events", a.cfg.StatePath("events.db"), "publishFeed", *publish)
	var wg sync.WaitGroup
	for _, d := range []*event.Dispatcher{fast, agents} {
		wg.Add(1)
		go func(d *event.Dispatcher) { defer wg.Done(); _ = d.Run(ctx) }(d)
	}
	if *publish {
		wg.Add(1)
		go func() { defer wg.Done(); a.feedLoop(ctx, info) }()
	}
	wg.Wait()
	return nil
}

// feedLoop publishes at most every 15s, and only when events or telemetry changed.
func (a *app) feedLoop(ctx context.Context, info feed.Pipeline) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	var lastE, lastT int64 = -1, -1
	for {
		if e, tl, err := a.store.Head(ctx); err == nil && (e != lastE || tl != lastT) {
			if err := a.publishFeed(ctx, info); err != nil {
				a.log.Error("feed publish", "err", err)
			} else {
				lastE, lastT = e, tl
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *app) load(ctx context.Context, key string) (*work.State, error) {
	st, err := work.Load(ctx, a.store, a.cfg.Tenant, key)
	if err != nil {
		return nil, err
	}
	if st.Key == "" {
		return nil, fmt.Errorf("no work item %s", key)
	}
	return st, nil
}

func (a *app) planShow(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "show" {
		return errors.New("usage: ap plan show <KEY> [-v N] [--diff]")
	}
	fs := flag.NewFlagSet("plan show", flag.ContinueOnError)
	ver := fs.Int("v", 0, "plan version (default latest)")
	diff := fs.Bool("diff", false, "diff against the previous version")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	st, err := a.load(ctx, args[1])
	if err != nil {
		return err
	}
	p := st.Latest()
	if *ver > 0 {
		p = st.Plan(*ver)
	}
	if p == nil {
		return fmt.Errorf("%s has no plan yet (status %s)", st.Key, st.Status)
	}
	if *diff {
		prev := st.Plan(p.Version - 1)
		if prev == nil {
			return fmt.Errorf("no v%d to diff against", p.Version-1)
		}
		fmt.Print(work.DiffPlans(st, prev, p))
		return nil
	}
	fmt.Print(work.RenderPlan(st, p))
	if st.Approved != nil && st.Approved.Version == p.Version {
		fmt.Printf("\nAPPROVED by %s\n", st.Approved.Approver)
	}
	return nil
}

func (a *app) status(ctx context.Context, key string) error {
	st, err := a.load(ctx, key)
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s\n  status   %s\n  sponsor  %s\n", st.Key, st.Card.Summary, st.Status, st.Card.Sponsor)
	if st.Reason != "" {
		fmt.Printf("  reason   %s\n", st.Reason)
	}
	for _, r := range st.Repos {
		fmt.Printf("  repo     %-5s %s\n", r.Component, r.Repo)
	}
	for _, p := range st.Plans {
		mark := ""
		if st.Approved != nil && st.Approved.Version == p.Version {
			mark = "  ✅ approved by " + st.Approved.Approver
		}
		fmt.Printf("  plan     v%d %s%s\n", p.Version, p.Digest[:19], mark)
	}
	fmt.Printf("  guidance %d  revisions %d  events %d  agent cost $%.2f\n", len(st.Guidance), len(st.Revisions), len(st.Events), st.CostUSD)
	return nil
}

func (a *app) timeline(ctx context.Context, key string) error {
	evs, err := a.store.Correlation(ctx, work.CorrelationID(a.cfg.Tenant, key))
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		return fmt.Errorf("no events for %s", key)
	}
	children := map[string][]*event.Event{}
	ids := map[string]bool{}
	for _, e := range evs {
		ids[e.ID] = true
	}
	var roots []*event.Event
	for _, e := range evs {
		if e.CausationID == "" || !ids[e.CausationID] {
			roots = append(roots, e)
		} else {
			children[e.CausationID] = append(children[e.CausationID], e)
		}
	}
	var walk func(e *event.Event, depth int)
	walk = func(e *event.Event, depth int) {
		fmt.Printf("%s  %s%s%s\n", e.Time.Format("15:04:05"), strings.Repeat("  ", depth), strings.TrimPrefix(strings.TrimSuffix(e.Type, ".v1"), "ap."), summary(e))
		kids := children[e.ID]
		sort.SliceStable(kids, func(i, j int) bool { return kids[i].Seq < kids[j].Seq })
		for _, k := range kids {
			walk(k, depth+1)
		}
	}
	fmt.Printf("correlation %s (%d events)\n", evs[0].CorrelationID, len(evs))
	for _, r := range roots {
		walk(r, 0)
	}
	return nil
}

// summary renders a few key data fields for the timeline.
func summary(e *event.Event) string {
	var m map[string]any
	if e.Decode(&m) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"author", "command", "reason", "agent", "version", "component", "result", "conclusion", "costUsd", "approver"} {
		if v, ok := m[k]; ok && v != "" && v != nil {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if b, ok := m["body"].(string); ok {
		if len(b) > 60 {
			b = b[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%q", b))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  · " + strings.Join(parts, " ")
}

func (a *app) events(ctx context.Context, n int) error {
	all, err := a.store.After(ctx, 0, nil, 1_000_000)
	if err != nil {
		return err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, e := range all {
		fmt.Printf("%5d %s %-42s %-14s %s\n", e.Seq, e.Time.Format("15:04:05"), e.Type, e.Subject, filepath.Base(e.Source))
	}
	return nil
}
