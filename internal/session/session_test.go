package session

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

func TestAssertionsAndTestPicking(t *testing.T) {
	src := "test('x', () => { expect(a).toBe(1); assert.equal(b, 2); assert(c) })\nok(d)"
	if n := Assertions(src); n != 4 {
		t.Fatalf("Assertions = %d, want 4", n)
	}
	got := pickTests([]string{"test/catalog.test.mjs", "test/fixtures/x.json", "web/src/lib/catalog.test.ts", "schema/a.json", "package.json"}, []string{"package.json"})
	want := "test/catalog.test.mjs test/fixtures/x.json web/src/lib/catalog.test.ts package.json"
	if strings.Join(got, " ") != want {
		t.Fatalf("pickTests = %v", got)
	}
}

// TestGuard checks the Scope Guard against a real git workspace: out-of-envelope,
// forbidden and test-weakening changes are reverted; the rest is kept.
func TestGuard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write("README.md", "x")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	write("test/a.test.mjs", "expect(1); expect(2); expect(3)")
	run("add", "-A")
	run("commit", "-q", "-m", "tests")
	testsSHA := run("rev-parse", "HEAD")

	// The "coding agent" weakens a test, writes inside and outside the envelope,
	// touches a forbidden path and creates node_modules.
	write("test/a.test.mjs", "expect(1)")
	write("src/ok.js", "ok")
	write("server/main.go", "package main")
	write(".github/workflows/x.yml", "x")
	write("node_modules/dep/index.js", "x")

	cat, _ := event.LoadCatalog(filepath.Join("..", "..", "demo", "catalog.yaml"))
	st, err := event.Open(filepath.Join(t.TempDir(), "e.db"), cat)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{Components: map[string]config.Component{
		"c": {Repo: "o/r", Paths: []string{"**"}, Forbidden: []string{".github/**", "server/**"}},
	}}
	h := Orchestrator{&Deps{Store: st, Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	cause, _ := event.New(event.Spec{Type: work.TItemAssigned, Subject: "work/X", CorrelationID: "wi-t-X"})
	it := &Item{Members: map[string]*Member{"c": {Component: "c", PathGlobs: []string{"src/**", "test/**"}}}}
	ws := &Workspace{Dir: dir}

	keep, err := h.guard(ctx, cause, it, "c", ws, []string{"test/a.test.mjs"}, testsSHA)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(keep, ",") != "src/ok.js" {
		t.Fatalf("kept %v, want only src/ok.js", keep)
	}
	if got := ws.ReadFile("test/a.test.mjs"); Assertions(got) != 3 {
		t.Fatalf("weakened test not restored: %q", got)
	}
	for _, p := range []string{"server/main.go", ".github/workflows/x.yml"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s should have been reverted", p)
		}
	}
}

// TestDeployOutcome checks that a failed deploy blocks the work item and
// escalates instead of completing it, and that a successful re-run completes it.
func TestDeployOutcome(t *testing.T) {
	ctx := context.Background()
	cat, err := event.LoadCatalog(filepath.Join("..", "..", "demo", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := event.Open(filepath.Join(t.TempDir(), "e.db"), cat)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := Coordinator{&Deps{Store: st, Cfg: &config.Config{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	root, _ := event.New(event.Spec{Type: work.TItemAssigned, Subject: "work/X", CorrelationID: "wi-t-X"})
	deploy := func(key, conclusion string, attempt int) []*event.Event {
		t.Helper()
		e, _ := emitFrom(root, TDeployed, key, Deployed{Component: "web", Repo: "o/r", SHA: "abc", Environment: "dev", Conclusion: conclusion, URL: "u", Attempt: attempt})
		if _, err := st.Publish(ctx, e); err != nil {
			t.Fatal(err)
		}
		out, err := h.Handle(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Publish(ctx, out...); err != nil {
			t.Fatal(err)
		}
		return out
	}
	types := func(evs []*event.Event) string {
		var s []string
		for _, e := range evs {
			s = append(s, e.Type)
		}
		return strings.Join(s, ",")
	}
	state := func() (*Item, *work.State) {
		t.Helper()
		evs, err := st.Correlation(ctx, "wi-t-X")
		if err != nil {
			t.Fatal(err)
		}
		it, err := Fold(evs)
		if err != nil {
			t.Fatal(err)
		}
		return it, it.Work
	}
	if _, err := st.Publish(ctx, root); err != nil {
		t.Fatal(err)
	}

	if got, want := types(deploy("1", "failure", 1)), work.TAgentEscalated+","+work.TWorkBlocked; got != want {
		t.Fatalf("failed deploy emitted %s, want %s", got, want)
	}
	if it, ws := state(); it.Done || it.Deployed || ws.Status != work.StBlocked {
		t.Fatalf("after failed deploy: done=%v deployed=%v status=%s", it.Done, it.Deployed, ws.Status)
	}

	if got := types(deploy("1-2", "success", 2)); got != TWorkCompleted {
		t.Fatalf("successful re-run emitted %s, want %s", got, TWorkCompleted)
	}
	if it, ws := state(); !it.Done || !it.Deployed || ws.Status != work.StDone {
		t.Fatalf("after re-run: done=%v deployed=%v status=%s", it.Done, it.Deployed, ws.Status)
	}
}
