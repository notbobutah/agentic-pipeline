// Package session implements coding sessions (docs/14 §6): a host-side git
// workspace per (work item, repo), agent tasks that edit it inside the
// sandbox, the Scope Guard that checks every change against the approved
// plan, and the Applier that commits and pushes. The sandbox never holds
// GitHub credentials; only this package pushes.
package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Dependency and build output never count as changes and are never committed.
var platformIgnore = []string{"node_modules/**", "**/node_modules/**", ".next/**", "**/.next/**", "**/next-env.d.ts", "**/*.tsbuildinfo"}

// Workspace is a git checkout owned by one coding session.
type Workspace struct {
	Dir    string // absolute path
	Repo   string // owner/name
	Branch string
}

func (w *Workspace) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", w.Dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Prepare clones the repo (once) and checks out the session branch, creating
// it from the default branch if it does not exist remotely yet.
func (w *Workspace) Prepare(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(w.Dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(w.Dir), 0o755); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "https://github.com/"+w.Repo+".git", w.Dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("clone %s: %v: %s", w.Repo, err, out)
		}
		for _, kv := range [][2]string{
			{"user.name", "pipeline-agent"},
			{"user.email", "pipeline-agent@users.noreply.github.com"},
			{"core.hooksPath", "/dev/null"},
		} {
			if _, err := w.git(ctx, "config", kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	if _, err := w.git(ctx, "fetch", "--quiet", "origin"); err != nil {
		return err
	}
	if _, err := w.git(ctx, "rev-parse", "--verify", "-q", "origin/"+w.Branch); err == nil {
		_, err = w.git(ctx, "checkout", "-q", "-B", w.Branch, "origin/"+w.Branch)
		return err
	}
	if _, err := w.git(ctx, "rev-parse", "--verify", "-q", w.Branch); err == nil {
		_, err = w.git(ctx, "checkout", "-q", w.Branch)
		return err
	}
	_, err := w.git(ctx, "checkout", "-q", "-b", w.Branch, "origin/HEAD")
	return err
}

// Changed lists paths changed since HEAD (modified, added, deleted,
// untracked), excluding platform-ignored paths. Each entry is "status path".
func (w *Workspace) Changed(ctx context.Context) ([]Change, error) {
	// -z: NUL-separated, no quoting, no trimming hazards (a leading space is
	// part of the status code).
	cmd := exec.CommandContext(ctx, "git", "-C", w.Dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	var cs []Change
	entries := strings.Split(string(raw), "\x00")
	for i := 0; i < len(entries); i++ {
		line := entries[i]
		if len(line) < 4 {
			continue
		}
		code, path := line[:2], line[3:]
		if code[0] == 'R' || code[0] == 'C' { // rename/copy: next entry is the source path
			i++
		}
		if matchAny(platformIgnore, path) {
			continue
		}
		cs = append(cs, Change{Path: path, Deleted: strings.Contains(code, "D")})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Path < cs[j].Path })
	return cs, nil
}

type Change struct {
	Path    string
	Deleted bool
}

// Revert discards changes to the given paths (restoring tracked files,
// removing untracked ones).
func (w *Workspace) Revert(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if _, err := w.git(ctx, "checkout", "-q", "HEAD", "--", p); err != nil {
			_ = os.RemoveAll(filepath.Join(w.Dir, p))
		}
	}
	return nil
}

// Commit stages the given paths and commits. Returns the new SHA.
func (w *Workspace) Commit(ctx context.Context, paths []string, message string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("nothing to commit")
	}
	args := append([]string{"add", "-A", "--"}, paths...)
	if _, err := w.git(ctx, args...); err != nil {
		return "", err
	}
	if _, err := w.git(ctx, "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	return w.git(ctx, "rev-parse", "HEAD")
}

// Push pushes the session branch.
func (w *Workspace) Push(ctx context.Context) error {
	_, err := w.git(ctx, "push", "-q", "-u", "origin", w.Branch)
	return err
}

// Head returns the current commit.
func (w *Workspace) Head(ctx context.Context) (string, error) { return w.git(ctx, "rev-parse", "HEAD") }

// ReadFile reads a file relative to the workspace (empty if missing).
func (w *Workspace) ReadFile(p string) string {
	b, _ := os.ReadFile(filepath.Join(w.Dir, p))
	return string(b)
}

// FileAt reads a file at a revision (empty if missing).
func (w *Workspace) FileAt(ctx context.Context, rev, p string) string {
	out, err := w.git(ctx, "show", rev+":"+p)
	if err != nil {
		return ""
	}
	return out
}

var assertion = regexp.MustCompile(`\b(expect|assert(?:\.\w+)?|t\.(?:is|ok|equal|deepEqual|throws|assert)|ok|strictEqual|deepStrictEqual)\s*\(`)

// Assertions counts assertion calls in source text: the Scope Guard's cheap
// check that tests were not weakened (docs/14 §6.2).
func Assertions(src string) int { return len(assertion.FindAllStringIndex(src, -1)) }

// ---- glob matching (same semantics as work.Match) ----

func matchAny(globs []string, file string) bool {
	for _, g := range globs {
		if matchSegs(strings.Split(g, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

func matchSegs(g, f []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			for i := 0; i <= len(f); i++ {
				if matchSegs(g[1:], f[i:]) {
					return true
				}
			}
			return false
		}
		if len(f) == 0 {
			return false
		}
		if ok, _ := filepath.Match(g[0], f[0]); !ok {
			return false
		}
		g, f = g[1:], f[1:]
	}
	return len(f) == 0
}
