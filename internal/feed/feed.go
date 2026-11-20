// Package feed publishes read-only JSON snapshots of the pipeline for the
// public /pipeline page: an index of work items and one file per item with its
// full event history and agent telemetry (enough for live view and replay).
//
// Snapshots are written to a directory and, optionally, force-pushed as a
// single commit to an orphan branch so the page can fetch them from
// raw.githubusercontent.com. Only public demo data goes in: cards, plans,
// events and agent progress, never credentials.
package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

const Version = 1

type HandlerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Pipeline struct {
	Name         string        `json:"name"`
	Repo         string        `json:"repo"`
	CatalogTypes int           `json:"catalogTypes"`
	Handlers     []HandlerInfo `json:"handlers"`
	Components   []Component   `json:"components"`
}

type Component struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
	Root string `json:"root"`
}

type CardSummary struct {
	Key        string   `json:"key"`
	Summary    string   `json:"summary"`
	Status     string   `json:"status"`
	Sponsor    string   `json:"sponsor"`
	Components []string `json:"components"`
	CostUSD    float64  `json:"costUsd"`
	Started    string   `json:"started"`
	Updated    string   `json:"updated"`
	Events     int      `json:"events"`
	File       string   `json:"file"`
}

type Index struct {
	Version     int           `json:"version"`
	GeneratedAt string        `json:"generatedAt"`
	Pipeline    Pipeline      `json:"pipeline"`
	Cards       []CardSummary `json:"cards"`
}

type FeedEvent struct {
	Seq         int64           `json:"seq"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Time        string          `json:"time"`
	Subject     string          `json:"subject"`
	CausationID string          `json:"causationId"`
	Source      string          `json:"source"`
	Actor       *event.Actor    `json:"actor,omitempty"`
	Data        json.RawMessage `json:"data"`
}

type CardFile struct {
	Version   int                 `json:"version"`
	Key       string              `json:"key"`
	Card      work.Card           `json:"card"`
	Status    string              `json:"status"`
	Reason    string              `json:"reason,omitempty"`
	Repos     []work.ResolvedRepo `json:"repos"`
	Guidance  []work.Guidance     `json:"guidance"`
	Plans     []work.PlanProposed `json:"plans"`
	Approved  *work.PlanApproved  `json:"approved,omitempty"`
	CostUSD   float64             `json:"costUsd"`
	Events    []FeedEvent         `json:"events"`
	Telemetry []event.Telemetry   `json:"telemetry"`
}

const ts = "2006-01-02T15:04:05.000Z07:00"

// Build writes index.json and cards/<KEY>.json into dir.
func Build(ctx context.Context, st *event.Store, cfg *config.Config, p Pipeline, dir string) (*Index, error) {
	corrs, err := st.Correlations(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "cards"), 0o755); err != nil {
		return nil, err
	}
	idx := &Index{Version: Version, GeneratedAt: time.Now().UTC().Format(ts), Pipeline: p}
	for corr := range corrs {
		if !strings.HasPrefix(corr, "wi-") {
			continue
		}
		evs, err := st.Correlation(ctx, corr)
		if err != nil {
			return nil, err
		}
		s, err := work.Replay(evs)
		if err != nil || s.Key == "" {
			continue
		}
		tel, err := st.TelemetryFor(ctx, corr)
		if err != nil {
			return nil, err
		}
		cf := CardFile{
			Version: Version, Key: s.Key, Card: s.Card, Status: string(s.Status), Reason: s.Reason,
			Repos: s.Repos, Guidance: s.Guidance, Plans: s.Plans, Approved: s.Approved, CostUSD: s.CostUSD,
			Telemetry: tel,
		}
		for _, e := range evs {
			cf.Events = append(cf.Events, FeedEvent{
				Seq: e.Seq, ID: e.ID, Type: e.Type, Time: e.Time.Format(ts), Subject: e.Subject,
				CausationID: e.CausationID, Source: e.Source, Actor: e.Actor, Data: e.Data,
			})
		}
		file := "cards/" + s.Key + ".json"
		if err := writeJSON(filepath.Join(dir, file), cf); err != nil {
			return nil, err
		}
		idx.Cards = append(idx.Cards, CardSummary{
			Key: s.Key, Summary: s.Card.Summary, Status: string(s.Status), Sponsor: s.Card.Sponsor,
			Components: s.Card.Components, CostUSD: s.CostUSD, Started: evs[0].Time.Format(ts),
			Updated: s.Updated.Format(ts), Events: len(evs), File: file,
		})
	}
	sort.Slice(idx.Cards, func(i, j int) bool { return idx.Cards[i].Started > idx.Cards[j].Started })
	return idx, writeJSON(filepath.Join(dir, "index.json"), idx)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Publisher pushes snapshots to an orphan branch as one amended commit, so the
// branch never accumulates history.
type Publisher struct {
	Dir    string // local git worktree for the feed branch
	Remote string // e.g. https://github.com/notbobutah/agentic-pipeline.git
	Branch string // e.g. feed
}

func (p *Publisher) git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", p.Dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Init prepares the worktree (idempotent).
func (p *Publisher) Init(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", p.Branch},
		{"remote", "add", "origin", p.Remote},
		{"config", "user.name", "agentic-pipeline feed"},
		{"config", "user.email", "feed@agentic-pipeline.invalid"},
		{"config", "core.hooksPath", "/dev/null"},
	} {
		if err := p.git(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// Publish commits the worktree contents and force-pushes them.
func (p *Publisher) Publish(ctx context.Context) error {
	if err := p.git(ctx, "add", "-A"); err != nil {
		return err
	}
	msg := "Pipeline feed snapshot " + time.Now().UTC().Format(time.RFC3339)
	amend := []string{"commit", "-q", "--allow-empty", "-m", msg}
	if p.git(ctx, "rev-parse", "--verify", "-q", "HEAD") == nil {
		amend = []string{"commit", "-q", "--amend", "--allow-empty", "-m", msg}
	}
	if err := p.git(ctx, amend...); err != nil {
		return err
	}
	return p.git(ctx, "push", "-q", "-f", "origin", "HEAD:"+p.Branch)
}
