// Package gh is a thin wrapper over the GitHub CLI for the demo: the Applier
// (branches, PRs) and the poller (checks, merges, deployments) use it. In the
// full design this is the GitHub App + Reporter; here the host's `gh` login is
// the bot identity.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return out, fmt.Errorf("gh %s: %v: %s", strings.Join(args[:min(len(args), 3)], " "), err, stderr)
	}
	return out, nil
}

func api(ctx context.Context, v any, path string, extra ...string) error {
	out, err := run(ctx, append([]string{"api", path}, extra...)...)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(out, v)
}

type PR struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	HTMLURL        string `json:"html_url"`
	Head           struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	MergedBy *struct {
		Login string `json:"login"`
	} `json:"merged_by"`
}

// GetPR fetches a pull request.
func GetPR(ctx context.Context, repo string, n int) (*PR, error) {
	var pr PR
	return &pr, api(ctx, &pr, fmt.Sprintf("repos/%s/pulls/%d", repo, n))
}

// FindPR returns the open PR for a head branch, or nil.
func FindPR(ctx context.Context, repo, branch string) (*PR, error) {
	var prs []PR
	owner := strings.Split(repo, "/")[0]
	if err := api(ctx, &prs, fmt.Sprintf("repos/%s/pulls?state=all&head=%s:%s", repo, owner, branch)); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// CreateDraftPR opens a draft PR and returns its number.
func CreateDraftPR(ctx context.Context, repo, head, base, title, body string, labels []string) (*PR, error) {
	args := []string{"api", "repos/" + repo + "/pulls", "-X", "POST",
		"-f", "head=" + head, "-f", "base=" + base, "-f", "title=" + title, "-f", "body=" + body, "-F", "draft=true"}
	out, err := run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var pr PR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, err
	}
	for _, l := range labels {
		_, _ = run(ctx, "api", "repos/"+repo+"/labels", "-X", "POST", "-f", "name="+l, "-f", "color=8250df")
		_, _ = run(ctx, "api", fmt.Sprintf("repos/%s/issues/%d/labels", repo, pr.Number), "-X", "POST", "-f", "labels[]="+l)
	}
	return &pr, nil
}

// MarkReady takes a PR out of draft.
func MarkReady(ctx context.Context, repo string, n int) error {
	_, err := run(ctx, "pr", "ready", fmt.Sprint(n), "-R", repo)
	if err != nil && strings.Contains(err.Error(), "already") {
		return nil
	}
	return err
}

// Comment adds a PR comment.
func Comment(ctx context.Context, repo string, n int, body string) error {
	_, err := run(ctx, "api", fmt.Sprintf("repos/%s/issues/%d/comments", repo, n), "-X", "POST", "-f", "body="+body)
	return err
}

// RequestReview asks users to review (best effort; a PR author can't be requested).
func RequestReview(ctx context.Context, repo string, n int, users ...string) error {
	args := []string{"api", fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", repo, n), "-X", "POST"}
	for _, u := range users {
		args = append(args, "-f", "reviewers[]="+u)
	}
	_, err := run(ctx, args...)
	return err
}

type CheckRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`     // queued | in_progress | completed
	Conclusion  string `json:"conclusion"` // success | failure | cancelled | skipped | ...
	HTMLURL     string `json:"html_url"`
	DetailsURL  string `json:"details_url"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

// Checks lists check runs for a commit.
func Checks(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	var r struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	return r.CheckRuns, api(ctx, &r, fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, sha))
}

// FailedLog returns the tail of a failed Actions job's log.
func FailedLog(ctx context.Context, repo string, jobID int64, lines int) string {
	out, err := run(ctx, "api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID))
	if err != nil {
		return "(log unavailable: " + err.Error() + ")"
	}
	all := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	// Keep the interesting end; strip timestamps for readability.
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	for i, l := range all {
		if len(l) > 29 && l[4] == '-' && l[28] == 'Z' {
			all[i] = l[29:]
		}
	}
	return strings.Join(all, "\n")
}

type WorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	HTMLURL    string `json:"html_url"`
	UpdatedAt  string `json:"updated_at"`
}

// Runs lists recent runs of a workflow file.
func Runs(ctx context.Context, repo, workflow string) ([]WorkflowRun, error) {
	var r struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	return r.WorkflowRuns, api(ctx, &r, fmt.Sprintf("repos/%s/actions/workflows/%s/runs?per_page=20", repo, workflow))
}

// JobSteps returns the failed step names of a job.
func FailedSteps(ctx context.Context, repo string, jobID int64) []string {
	var j struct {
		Steps []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
		} `json:"steps"`
	}
	if api(ctx, &j, fmt.Sprintf("repos/%s/actions/jobs/%d", repo, jobID)) != nil {
		return nil
	}
	var out []string
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			out = append(out, s.Name)
		}
	}
	return out
}

// API calls a REST path and decodes the JSON response into v.
func API(ctx context.Context, v any, path string) error { return api(ctx, v, path) }
