package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/agent"
	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/gh"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

// Execer runs deterministic commands with the sandbox toolchain.
type Execer interface {
	Exec(ctx context.Context, hostDir, script string) (string, error)
}

// Deps are shared by the session handlers and the poller.
type Deps struct {
	Store   *event.Store
	Cfg     *config.Config
	Agent   agent.Runner
	Sandbox Execer
	Log     *slog.Logger
}

func (d *Deps) item(ctx context.Context, corr string) (*Item, []*event.Event, error) {
	evs, err := d.Store.Correlation(ctx, corr)
	if err != nil {
		return nil, nil, err
	}
	it, err := Fold(evs)
	return it, evs, err
}

// publish emits keyed events immediately (sessions are long; the timeline
// should show progress as it happens, not when the handler returns).
func (d *Deps) publish(ctx context.Context, cause *event.Event, typ, key string, data any) (*event.Event, error) {
	e, err := event.New(event.Spec{
		Type: typ, Subject: cause.Subject, Source: "//ap/sessions", Cause: cause, Key: key, Data: data,
		Origin: "system", Actor: &event.Actor{Kind: "system", ID: "agentic-pipeline"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := d.Store.Publish(ctx, e); err != nil {
		return nil, err
	}
	d.Log.Info("emit", "type", typ, "subject", cause.Subject, "key", key)
	return e, nil
}

func sessionID(key, comp string) string  { return "ses-" + strings.ToLower(key) + "-" + comp }
func branchName(key, comp string) string { return "ap/" + key + "/" + comp }

// Orchestrator is H-WRK-06: it runs coding sessions in dependency order,
// re-pins consumers when a producer moves, and iterates on feedback.
type Orchestrator struct{ *Deps }

func (h Orchestrator) Name() string { return "H-WRK-06.session-orchestrator" }
func (h Orchestrator) Types() []string {
	return []string{TRequested, TCommitPushed, TFeedback, TMerged}
}

func (h Orchestrator) Handle(ctx context.Context, e *event.Event) ([]*event.Event, error) {
	it, _, err := h.item(ctx, e.CorrelationID)
	if err != nil {
		return nil, err
	}
	if it.Done || it.Work.Status == work.StCancelled {
		return nil, nil
	}
	switch e.Type {
	case TRequested:
		var d work.SessionRequested
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		pin, wait := h.pinFor(it, d.Component)
		if wait {
			h.Log.Info("session waits for its producer", "component", d.Component)
			return nil, nil
		}
		return nil, h.start(ctx, it, d.Component, pin)

	case TCommitPushed:
		var d CommitPushed
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		if d.Kind != "implementation" && d.Kind != "fix" {
			return nil, nil
		}
		for c, m := range it.Members {
			if _, consumes := h.Cfg.Components[c].DependsOn[d.Component]; !consumes || m.Merged || m.Requested == nil {
				continue
			}
			if !m.Started {
				if err := h.start(ctx, it, c, d.SHA); err != nil {
					return nil, err
				}
			} else if m.ImplDone {
				if err := h.repin(ctx, it, c, d.Component, d.SHA); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil

	case TMerged:
		var d Merged
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		for c, m := range it.Members {
			if _, consumes := h.Cfg.Components[c].DependsOn[d.Component]; consumes && m.ImplDone && !m.Merged {
				if err := h.repin(ctx, it, c, d.Component, d.MergeSHA); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil

	case TFeedback:
		var d Feedback
		if err := e.Decode(&d); err != nil {
			return nil, err
		}
		return nil, h.iterate(ctx, it, e, d)
	}
	return nil, nil
}

// pinFor returns the producer SHA a consumer must build against, or wait=true
// if the producer has not pushed an implementation yet.
func (h Orchestrator) pinFor(it *Item, comp string) (string, bool) {
	for prod := range h.Cfg.Components[comp].DependsOn {
		p, ok := it.Members[prod]
		if !ok {
			continue // producer not part of this change: use whatever is pinned
		}
		if p.Merged {
			return p.MergeSHA, false
		}
		if !p.ImplDone {
			return "", true
		}
		return p.HeadSHA, false
	}
	return "", false
}

func (h Orchestrator) workspace(key, comp string) *Workspace {
	dir, _ := filepath.Abs(h.Cfg.StatePath("sessions", key, comp))
	return &Workspace{Dir: dir, Repo: h.Cfg.Components[comp].Repo, Branch: branchName(key, comp)}
}

// start runs a session from the beginning (or resumes after a crash):
// Test Author → guard → commit; Coding agent → guard → commit; push; draft PR.
func (h Orchestrator) start(ctx context.Context, it *Item, comp, pin string) error {
	m := it.Members[comp]
	key := it.Work.Key
	sid := sessionID(key, comp)
	ws := h.workspace(key, comp)
	cause := m.Requested
	if _, err := h.publish(ctx, cause, TStarted, "started", Started{SessionID: sid, Component: comp, Repo: ws.Repo, Branch: ws.Branch, PinSHA: pin}); err != nil {
		return err
	}
	if err := ws.Prepare(ctx); err != nil {
		return h.block(ctx, cause, sid, comp, "workspace", err.Error())
	}
	plan := approvedPlan(it)
	if plan == nil {
		return h.block(ctx, cause, sid, comp, "no-approved-plan", "session started without an approved plan")
	}
	testFiles := m.TestFiles
	testsSHA := ""
	if !m.TestsDone {
		res, err := h.runAgent(ctx, cause, it, comp, "test-author", testAuthorPrompt(it, plan, comp, pin, h.Cfg), testAuthorSchema(), ws)
		if err != nil {
			return h.block(ctx, cause, sid, comp, "test-author-failed", err.Error())
		}
		files, err := h.guard(ctx, cause, it, comp, ws, nil, "")
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return h.block(ctx, cause, sid, comp, "no-tests", "the Test Author produced no changes")
		}
		testFiles = pickTests(files, res.StringList("testFiles"))
		sha, err := h.commitPush(ctx, cause, it, comp, ws, files, "tests",
			fmt.Sprintf("test(%s): %s", key, firstLine(res.String("summary"), "tests for the approved plan")), 0, testFiles)
		if err != nil {
			return err
		}
		testsSHA = sha
	} else {
		testsSHA = testsCommit(it, comp)
	}

	if !m.ImplDone {
		res, err := h.runAgent(ctx, cause, it, comp, "coding", codingPrompt(it, plan, comp, pin, testFiles, h.Cfg), codingSchema(), ws)
		if err != nil {
			return h.block(ctx, cause, sid, comp, "coding-failed", err.Error())
		}
		files, err := h.guard(ctx, cause, it, comp, ws, testFiles, testsSHA)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return h.block(ctx, cause, sid, comp, "no-implementation", "the Coding agent produced no changes")
		}
		if _, err := h.commitPush(ctx, cause, it, comp, ws, files, "implementation",
			fmt.Sprintf("feat(%s): %s", key, firstLine(res.String("commitMessage"), it.Work.Card.Summary)), 0, nil); err != nil {
			return err
		}
	}
	return h.openPR(ctx, cause, it, comp, ws, plan)
}

// iterate is one outer-loop fix: structured feedback in, a fix commit out.
func (h Orchestrator) iterate(ctx context.Context, it *Item, fb *event.Event, d Feedback) error {
	m := it.Members[d.Component]
	if m == nil || m.Merged || m.Blocked != "" {
		return nil
	}
	if d.HeadSHA != "" && d.HeadSHA != m.HeadSHA {
		return nil // feedback about a superseded commit
	}
	key := it.Work.Key
	sid := sessionID(key, d.Component)
	n := m.Iterations + 1
	max := h.Cfg.Sessions.MaxIterations
	if max == 0 {
		max = 5
	}
	if n > max {
		return h.block(ctx, fb, sid, d.Component, "iteration-limit", fmt.Sprintf("%d fixes did not get CI green; last failure: %s", max, d.Summary))
	}
	if _, err := h.publish(ctx, fb, TIteration, fmt.Sprint(n), Iteration{SessionID: sid, Component: d.Component, Iteration: n, Trigger: d.Source}); err != nil {
		return err
	}
	ws := h.workspace(key, d.Component)
	if err := ws.Prepare(ctx); err != nil {
		return h.block(ctx, fb, sid, d.Component, "workspace", err.Error())
	}
	plan := approvedPlan(it)
	res, err := h.runAgentRole(ctx, fb, it, d.Component, "coding-fix", "coding", fixPrompt(it, plan, d.Component, d, m.TestFiles, h.Cfg), codingSchema(), ws, n)
	if err != nil {
		return h.block(ctx, fb, sid, d.Component, "fix-failed", err.Error())
	}
	files, err := h.guard(ctx, fb, it, d.Component, ws, m.TestFiles, testsCommit(it, d.Component))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return h.block(ctx, fb, sid, d.Component, "no-fix", "the agent made no change in response to: "+d.Summary)
	}
	_, err = h.commitPush(ctx, fb, it, d.Component, ws, files, "fix",
		fmt.Sprintf("fix(%s): %s", key, firstLine(res.String("commitMessage"), d.Summary)), n, nil)
	return err
}

// repin updates a consumer's dependency on a producer to a new commit — a
// deterministic step (no agent), run with the sandbox toolchain.
func (h Orchestrator) repin(ctx context.Context, it *Item, comp, producer, sha string) error {
	m := it.Members[comp]
	dep := h.Cfg.Components[comp].DependsOn[producer]
	ws := h.workspace(it.Work.Key, comp)
	if err := ws.Prepare(ctx); err != nil {
		return err
	}
	spec := strings.ReplaceAll(dep.Spec, "{sha}", sha)
	root := h.Cfg.Components[comp].Root
	if root == "" {
		root = "."
	}
	if strings.Contains(ws.ReadFile(filepath.Join(root, "package.json")), spec) {
		return nil
	}
	script := fmt.Sprintf("cd %q && npm install --no-audit --no-fund --save %q", root, dep.Package+"@"+spec)
	if out, err := h.Sandbox.Exec(ctx, ws.Dir, script); err != nil {
		return h.block(ctx, m.Requested, sessionID(it.Work.Key, comp), comp, "repin-failed", out)
	}
	files, err := ws.Changed(ctx)
	if err != nil {
		return err
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	_, err = h.commitPush(ctx, m.Requested, it, comp, ws, paths, "repin",
		fmt.Sprintf("chore(%s): pin %s to %s", it.Work.Key, dep.Package, sha[:min(12, len(sha))]), 0, nil)
	return err
}

// guard is the Scope Guard (H-WRK-08): every change must be inside the
// approved scope envelope and outside forbidden paths, and the Test Author's
// tests may not be deleted or weakened. Violations are reverted and recorded
// as policy decisions; the remaining changes are returned for commit.
func (h Orchestrator) guard(ctx context.Context, cause *event.Event, it *Item, comp string, ws *Workspace, testFiles []string, testsSHA string) ([]string, error) {
	m := it.Members[comp]
	cc := h.Cfg.Components[comp]
	changes, err := ws.Changed(ctx)
	if err != nil {
		return nil, err
	}
	var keep, revert, reasons []string
	protected := map[string]bool{}
	for _, f := range testFiles {
		protected[f] = true
	}
	for _, c := range changes {
		switch {
		case matchAny(cc.Forbidden, c.Path):
			revert, reasons = append(revert, c.Path), append(reasons, c.Path+": forbidden path")
		case !matchAny(m.PathGlobs, c.Path):
			revert, reasons = append(revert, c.Path), append(reasons, c.Path+": outside the approved scope envelope")
		case protected[c.Path] && c.Deleted:
			revert, reasons = append(revert, c.Path), append(reasons, c.Path+": deletes a Test Author test")
		case protected[c.Path] && testsSHA != "" && Assertions(ws.ReadFile(c.Path)) < Assertions(ws.FileAt(ctx, testsSHA, c.Path)):
			revert, reasons = append(revert, c.Path), append(reasons, c.Path+": removes assertions from a Test Author test")
		default:
			keep = append(keep, c.Path)
		}
	}
	result := "allow"
	if len(revert) > 0 {
		result = "deny"
		if err := ws.Revert(ctx, revert); err != nil {
			return nil, err
		}
	}
	_, err = h.publish(ctx, cause, work.TPolicyDecision, "guard-"+comp+"-"+fmt.Sprint(len(changes))+"-"+hash(keep),
		work.PolicyDecision{Query: "scope.guard " + comp, Result: result, Reasons: append(reasons, fmt.Sprintf("%d change(s) within the envelope", len(keep)))})
	return keep, err
}

func (h Orchestrator) commitPush(ctx context.Context, cause *event.Event, it *Item, comp string, ws *Workspace, files []string, kind, subject string, iteration int, testFiles []string) (string, error) {
	key := it.Work.Key
	plan := it.Work.Approved
	msg := subject + "\n\n" +
		"Ap-Work-Item: " + key + "\n" +
		"Ap-Session-Id: " + sessionID(key, comp) + "\n" +
		fmt.Sprintf("Ap-Plan: %s@v%d\n", plan.PlanID, plan.Version) +
		"Ap-Sponsor: " + it.Work.Card.Sponsor + "\n"
	sha, err := ws.Commit(ctx, files, msg)
	if err != nil {
		return "", err
	}
	if err := ws.Push(ctx); err != nil {
		return "", err
	}
	_, err = h.publish(ctx, cause, TCommitPushed, sha, CommitPushed{
		SessionID: sessionID(key, comp), Component: comp, Repo: ws.Repo, Branch: ws.Branch, SHA: sha,
		Kind: kind, Files: files, Message: subject, Iteration: iteration, TestFiles: testFiles,
	})
	return sha, err
}

func (h Orchestrator) openPR(ctx context.Context, cause *event.Event, it *Item, comp string, ws *Workspace, plan *work.Plan) error {
	pr, err := gh.FindPR(ctx, ws.Repo, ws.Branch)
	if err != nil {
		return err
	}
	if pr == nil {
		title := fmt.Sprintf("[%s] %s (%s)", it.Work.Key, it.Work.Card.Summary, comp)
		pr, err = gh.CreateDraftPR(ctx, ws.Repo, ws.Branch, "main", title, prBody(it, plan, comp), []string{"ap-agent-authored"})
		if err != nil {
			return h.block(ctx, cause, sessionID(it.Work.Key, comp), comp, "open-pr", err.Error())
		}
	}
	_, err = h.publish(ctx, cause, TChangeOpened, fmt.Sprint(pr.Number), ChangeOpened{
		SessionID: sessionID(it.Work.Key, comp), Component: comp, Repo: ws.Repo, ChangeNumber: pr.Number, URL: pr.HTMLURL, Draft: true,
	})
	return err
}

func (h Orchestrator) block(ctx context.Context, cause *event.Event, sid, comp, reason, detail string) error {
	detail = tail(detail, 1500)
	if _, err := h.publish(ctx, cause, TBlocked, "blocked-"+comp+"-"+reason, Blocked{SessionID: sid, Component: comp, Reason: reason, Detail: detail}); err != nil {
		return err
	}
	if _, err := h.publish(ctx, cause, work.TAgentEscalated, "esc-"+comp+"-"+reason, work.Escalated{Reason: reason, Summary: comp + ": " + detail}); err != nil {
		return err
	}
	_, err := h.publish(ctx, cause, work.TWorkBlocked, "wb-"+comp+"-"+reason, work.Reason{Reason: reason, Detail: comp + ": " + detail})
	return err
}

// ---- agent invocation ----

type result struct{ m map[string]any }

func (r result) String(k string) string {
	s, _ := r.m[k].(string)
	return s
}

func (r result) StringList(k string) []string {
	var out []string
	if xs, ok := r.m[k].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (h Orchestrator) runAgent(ctx context.Context, cause *event.Event, it *Item, comp, role, prompt string, schema map[string]any, ws *Workspace) (result, error) {
	return h.runAgentRole(ctx, cause, it, comp, role, role, prompt, schema, ws, 0)
}

// Shell access for coding agents: the toolchain and read-only git, nothing
// that can reach GitHub or install system packages (permissionMode dontAsk
// denies everything not listed).
var codingTools = []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash"}
var codingAllowed = []string{
	"Read", "Write", "Edit", "Glob", "Grep",
	"Bash(npm:*)", "Bash(npx:*)", "Bash(node:*)", "Bash(cd:*)", "Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)",
	"Bash(tail:*)", "Bash(wc:*)", "Bash(find:*)", "Bash(mkdir:*)", "Bash(rm:*)", "Bash(mv:*)", "Bash(cp:*)",
	"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)",
}

func (h Orchestrator) runAgentRole(ctx context.Context, cause *event.Event, it *Item, comp, budgetRole, agentName, prompt string, schema map[string]any, ws *Workspace, iteration int) (result, error) {
	role := h.Cfg.Agents.Roles[budgetRole]
	taskID := fmt.Sprintf("%s-%s-%s", strings.ToLower(it.Work.Key), comp, budgetRole)
	if iteration > 0 {
		taskID += fmt.Sprintf("-i%d", iteration)
	}
	if _, err := h.publish(ctx, cause, work.TAgentTaskAssigned, taskID, work.AgentTask{
		TaskID: taskID, Agent: agentName, Model: h.Cfg.Agents.Model, MaxTurns: role.MaxTurns, MaxBudgetUSD: role.MaxBudgetUSD,
	}); err != nil {
		return result{}, err
	}
	res, runErr := h.Agent.Run(ctx, agent.Task{
		ID: taskID, Role: agentName, Model: h.Cfg.Agents.Model,
		SystemPrompt: sessionSystemPrompt, Prompt: prompt,
		Tools: codingTools, AllowedTools: codingAllowed,
		MaxTurns: role.MaxTurns, MaxBudgetUSD: role.MaxBudgetUSD, OutputSchema: schema,
		Repos: []agent.Repo{{Name: comp, HostDir: ws.Dir}},
		Progress: func(line []byte) {
			_ = h.Store.AddTelemetry(ctx, cause.CorrelationID, taskID, line)
		},
	})
	done := work.AgentTaskCompleted{TaskID: taskID, Conclusion: "failure"}
	var out result
	if res != nil {
		done.Subtype, done.CostUSD, done.Turns, done.DurationMs, done.Errors = res.Subtype, res.CostUSD, res.Turns, res.DurationMs, res.Errors
		if res.OK() && json.Unmarshal(res.Structured, &out.m) == nil {
			done.Conclusion = "success"
		}
	}
	if runErr != nil {
		done.Errors = append(done.Errors, runErr.Error())
	}
	if _, err := h.publish(ctx, cause, work.TAgentTaskCompleted, taskID, done); err != nil {
		return out, err
	}
	if done.Conclusion != "success" {
		return out, fmt.Errorf("%s %s", done.Subtype, strings.Join(done.Errors, "; "))
	}
	if _, err := h.publish(ctx, cause, work.TAgentProposal, taskID, work.Proposal{ProposalID: "prp-" + taskID, TaskID: taskID, Kind: "commit"}); err != nil {
		return out, err
	}
	if cmds := out.StringList("commandsRun"); len(cmds) > 0 {
		passing, _ := out.m["allPassing"].(bool)
		_, _ = h.publish(ctx, cause, TInnerLoop, taskID, InnerLoop{SessionID: sessionID(it.Work.Key, comp), Component: comp, Role: agentName, Commands: cmds, Passing: passing})
	}
	return out, nil
}

// ---- helpers ----

func approvedPlan(it *Item) *work.Plan {
	if it.Work.Approved == nil {
		return nil
	}
	p := it.Work.Plan(it.Work.Approved.Version)
	if p == nil {
		return nil
	}
	return &p.Plan
}

func testsCommit(it *Item, comp string) string {
	for _, e := range it.Work.Events {
		if e.Type == TCommitPushed {
			var d CommitPushed
			if e.Decode(&d) == nil && d.Component == comp && d.Kind == "tests" {
				return d.SHA
			}
		}
	}
	return ""
}

var testLike = regexp.MustCompile(`(^|/)(test|tests|__tests__|fixtures)/|\.(test|spec)\.[cm]?[jt]sx?$`)

// pickTests chooses which committed files the Test Author owns: what it
// reported, plus anything that looks like a test, limited to committed files.
func pickTests(committed, reported []string) []string {
	rep := map[string]bool{}
	for _, r := range reported {
		rep[strings.TrimPrefix(r, "./")] = true
	}
	var out []string
	for _, f := range committed {
		if rep[f] || testLike.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if s == "" {
		s = fallback
	}
	if len(s) > 72 {
		s = s[:69] + "..."
	}
	return s
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func hash(xs []string) string {
	h := 0
	for _, x := range xs {
		for _, c := range x {
			h = h*31 + int(c)
		}
	}
	return fmt.Sprintf("%x", uint32(h))
}
