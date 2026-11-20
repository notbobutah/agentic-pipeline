// Package agent is the Agent Gateway (docs/07 §5) for the demo. It runs one
// agent task in a disposable Docker sandbox: the Go side prepares a task
// directory (task.json + repo checkouts), the TypeScript runner inside the
// container drives the Claude Agent SDK and writes result.json.
//
// The sandbox never receives a GitHub token. Only Anthropic credentials go in.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Repo is a repository checkout made available to the agent. Either the
// gateway clones Slug (read-only planning), or HostDir is an existing writable
// workspace owned by a coding session.
type Repo struct {
	Name     string // directory name under /work/repos
	Slug     string // owner/name on GitHub
	Ref      string // branch or SHA; empty = default branch
	ReadOnly bool
	HostDir  string // existing checkout to mount read-write (coding sessions)
}

// Task is one agent invocation.
type Task struct {
	ID           string
	Role         string // implementation-planner | test-author | coding | diagnoser
	Model        string
	SystemPrompt string
	Prompt       string
	Tools        []string // tools that exist for the agent
	AllowedTools []string // permission rules auto-approved; empty = Tools. Anything else is denied.
	MaxTurns     int
	MaxBudgetUSD float64
	OutputSchema map[string]any
	Repos        []Repo

	// Progress receives each JSONL progress line the runner prints (tier-T
	// telemetry). Optional.
	Progress func(line []byte)
}

// Result is what the runner reports.
type Result struct {
	Subtype    string          `json:"subtype"` // success | error_max_turns | error_max_budget_usd | ...
	IsError    bool            `json:"isError"`
	Structured json.RawMessage `json:"structured"`
	Text       string          `json:"text"`
	CostUSD    float64         `json:"costUsd"`
	Turns      int             `json:"turns"`
	DurationMs int64           `json:"durationMs"`
	Denials    int             `json:"permissionDenials"`
	Errors     []string        `json:"errors"`
}

// OK reports whether the task produced a usable structured result.
func (r *Result) OK() bool {
	return r != nil && r.Subtype == "success" && !r.IsError && len(r.Structured) > 0 && string(r.Structured) != "null"
}

// Runner runs agent tasks.
type Runner interface {
	Run(ctx context.Context, t Task) (*Result, error)
}

// runnerTask is the task.json contract with runner/src/index.ts.
type runnerTask struct {
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	SystemPrompt string         `json:"systemPrompt"`
	Prompt       string         `json:"prompt"`
	Tools        []string       `json:"tools"`
	AllowedTools []string       `json:"allowedTools,omitempty"`
	Cwd          string         `json:"cwd"`
	MaxTurns     int            `json:"maxTurns"`
	MaxBudgetUSD float64        `json:"maxBudgetUsd"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// Docker runs tasks in containers built from sandbox/Dockerfile.
type Docker struct {
	Image    string
	Auth     string // api-key | claude-login
	StateDir string // task directories live under StateDir/tasks
	Log      *slog.Logger
	Timeout  time.Duration
}

func (d *Docker) Run(ctx context.Context, t Task) (*Result, error) {
	if d.Timeout == 0 {
		d.Timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()

	dir, err := filepath.Abs(filepath.Join(d.StateDir, "tasks", t.ID))
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "repos"), 0o755); err != nil {
		return nil, err
	}
	for _, r := range t.Repos {
		if r.HostDir != "" {
			if err := os.MkdirAll(filepath.Join(dir, "repos", r.Name), 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := clone(ctx, r, filepath.Join(dir, "repos", r.Name)); err != nil {
			return nil, err
		}
	}
	rt := runnerTask{
		Role: t.Role, Model: t.Model, SystemPrompt: t.SystemPrompt, Prompt: t.Prompt,
		Tools: t.Tools, AllowedTools: t.AllowedTools, Cwd: "/work/repos", MaxTurns: t.MaxTurns, MaxBudgetUSD: t.MaxBudgetUSD,
		OutputSchema: t.OutputSchema,
	}
	if err := writeJSON(filepath.Join(dir, "task.json"), rt); err != nil {
		return nil, err
	}

	args := []string{"run", "--rm", "--name", "ap-" + t.ID,
		"-v", dir + ":/work",
		"-e", "CLAUDE_AGENT_SDK_CLIENT_APP=agentic-pipeline/0.1",
	}
	for _, r := range t.Repos {
		switch {
		case r.HostDir != "":
			args = append(args, "-v", r.HostDir+":/work/repos/"+r.Name)
		case r.ReadOnly:
			args = append(args, "-v", filepath.Join(dir, "repos", r.Name)+":/work/repos/"+r.Name+":ro")
		}
	}
	cleanup, authArgs, err := d.auth(dir)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args = append(args, authArgs...)
	args = append(args, d.Image, "/work/task.json")

	d.Log.Info("agent start", "task", t.ID, "role", t.Role, "image", d.Image)
	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tail{max: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		d.Log.Debug("agent", "task", t.ID, "line", sc.Text())
		if t.Progress != nil {
			t.Progress(append([]byte(nil), sc.Bytes()...))
		}
	}
	waitErr := cmd.Wait()

	var res Result
	b, readErr := os.ReadFile(filepath.Join(dir, "result.json"))
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("agent container failed: %v: %s", waitErr, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("agent wrote no result.json: %w", readErr)
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("parse result.json: %w", err)
	}
	d.Log.Info("agent done", "task", t.ID, "subtype", res.Subtype, "turns", res.Turns, "costUsd", res.CostUSD)
	return &res, nil
}

// Exec runs a deterministic command (no agent, no credentials) in the sandbox
// image with hostDir mounted at /work/repo. Used for mechanical steps such as
// re-pinning a dependency, so they run with the same toolchain as agents.
func (d *Docker) Exec(ctx context.Context, hostDir, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", hostDir+":/work/repo", "-w", "/work/repo",
		"--entrypoint", "bash", d.Image, "-lc", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("sandbox exec: %v: %s", err, tailString(string(out), 2000))
	}
	return string(out), nil
}

func tailString(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// auth returns docker args that give the container Anthropic credentials, and
// a cleanup func. API keys are passed by name (-e NAME), never on the command line.
func (d *Docker) auth(dir string) (func(), []string, error) {
	noop := func() {}
	switch d.Auth {
	case "", "api-key":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return noop, nil, errors.New("agents.auth=api-key but ANTHROPIC_API_KEY is not set")
		}
		return noop, []string{"-e", "ANTHROPIC_API_KEY"}, nil
	case "claude-login":
		home, err := os.UserHomeDir()
		if err != nil {
			return noop, nil, err
		}
		src := filepath.Join(home, ".claude", ".credentials.json")
		b, err := os.ReadFile(src)
		if err != nil {
			return noop, nil, fmt.Errorf("agents.auth=claude-login: %w", err)
		}
		credDir := filepath.Join(dir, ".claude-home")
		if err := os.MkdirAll(credDir, 0o700); err != nil {
			return noop, nil, err
		}
		if err := os.WriteFile(filepath.Join(credDir, ".credentials.json"), b, 0o600); err != nil {
			return noop, nil, err
		}
		// A copy, so a token refresh inside the container never touches the host login.
		return func() { os.RemoveAll(credDir) }, []string{"-v", credDir + ":/home/node/.claude"}, nil
	default:
		return noop, nil, fmt.Errorf("unknown agents.auth %q", d.Auth)
	}
}

func clone(ctx context.Context, r Repo, dst string) error {
	args := []string{"clone", "--quiet", "--depth", "50"}
	if r.Ref != "" {
		args = append(args, "--branch", r.Ref)
	}
	args = append(args, "https://github.com/"+r.Slug+".git", dst)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s: %v: %s", r.Slug, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// tail keeps the last max bytes written.
type tail struct {
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }

// Fake returns canned results from fixture files (<dir>/<role>.json holding
// the structured output). For tests and dry runs: AP_FAKE_AGENT=1.
type Fake struct {
	Dir string
	Log *slog.Logger
}

func (f *Fake) Run(_ context.Context, t Task) (*Result, error) {
	b, err := os.ReadFile(filepath.Join(f.Dir, t.Role+".json"))
	if err != nil {
		return nil, fmt.Errorf("fake agent: %w", err)
	}
	if f.Log != nil {
		f.Log.Info("fake agent", "task", t.ID, "role", t.Role)
	}
	if t.Progress != nil {
		t.Progress([]byte(`{"kind":"status","status":"started","role":"` + t.Role + `","model":"fixture"}`))
		for _, tool := range []string{"Glob", "Read", "Read", "Grep"} {
			t.Progress([]byte(`{"kind":"tool","name":"` + tool + `"}`))
		}
		t.Progress([]byte(`{"kind":"status","status":"done","subtype":"success"}`))
	}
	return &Result{Subtype: "success", Structured: b, CostUSD: 0, Turns: 1}, nil
}

var _ io.Writer = (*tail)(nil)
