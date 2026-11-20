// Package work implements the work plane of docs/14 for the demo: card
// intake, guidance, implementation planning and developer plan approval.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// Card is a tracker work item (a Jira issue stand-in).
type Card struct {
	Key                string   `json:"key"`
	IssueType          string   `json:"issueType"`
	Summary            string   `json:"summary"`
	Assignee           string   `json:"assignee"`
	Sponsor            string   `json:"sponsor"`
	Priority           string   `json:"priority,omitempty"`
	Components         []string `json:"components"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	Guidance           []string `json:"guidance,omitempty"`
}

// LoadCard reads a card JSON file.
func LoadCard(path string) (*Card, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Card
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Key == "" || c.Summary == "" {
		return nil, fmt.Errorf("%s: card needs key and summary", path)
	}
	return &c, nil
}

// CorrelationID is the work item's root correlation (docs/03 §8).
func CorrelationID(tenant, key string) string { return "wi-" + tenant + "-" + key }

// Subject is the CloudEvents subject for work-item events.
func Subject(key string) string { return "work/" + key }

// ---- Implementation plan (docs/14 §5). Also the planner's output schema. ----

type FileChange struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // create | modify | delete
	Purpose string `json:"purpose"`
}

type RepoPlan struct {
	Component string       `json:"component"`
	Repo      string       `json:"repo"`
	Changes   []FileChange `json:"changes"`
}

type TestPlan struct {
	Name      string   `json:"name"`
	Component string   `json:"component"`
	Level     string   `json:"level"` // unit | integration | contract | build
	Covers    []string `json:"covers"`
}

type Envelope struct {
	Component string   `json:"component"`
	PathGlobs []string `json:"pathGlobs"`
}

type Estimate struct {
	Iterations       string  `json:"iterations"`
	CostUSD          float64 `json:"costUsd"`
	WallclockMinutes int     `json:"wallclockMinutes"`
}

type Plan struct {
	Summary       string     `json:"summary"`
	Approach      []string   `json:"approach"`
	Repos         []RepoPlan `json:"repos"`
	Tests         []TestPlan `json:"tests"`
	OutOfScope    []string   `json:"outOfScope"`
	Risks         []string   `json:"risks"`
	OpenQuestions []string   `json:"openQuestions"`
	ScopeEnvelope []Envelope `json:"scopeEnvelope"`
	Estimate      Estimate   `json:"estimate"`
}

// Digest is the content hash approval binds to (ADR-0007).
func (p *Plan) Digest() string {
	b, _ := json.Marshal(p) // struct field order is fixed → canonical enough
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PlanSchema is the JSON Schema handed to the planner as its output format.
func PlanSchema() map[string]any {
	str := map[string]any{"type": "string"}
	strs := map[string]any{"type": "array", "items": str}
	obj := func(req []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": req, "properties": props}
	}
	return obj(
		[]string{"summary", "approach", "repos", "tests", "outOfScope", "risks", "openQuestions", "scopeEnvelope", "estimate"},
		map[string]any{
			"summary":  str,
			"approach": strs,
			"repos": map[string]any{"type": "array", "items": obj([]string{"component", "repo", "changes"}, map[string]any{
				"component": str, "repo": str,
				"changes": map[string]any{"type": "array", "items": obj([]string{"path", "action", "purpose"}, map[string]any{
					"path": str, "action": map[string]any{"type": "string", "enum": []string{"create", "modify", "delete"}}, "purpose": str,
				})},
			})},
			"tests": map[string]any{"type": "array", "items": obj([]string{"name", "component", "level", "covers"}, map[string]any{
				"name": str, "component": str,
				"level":  map[string]any{"type": "string", "enum": []string{"unit", "integration", "contract", "build"}},
				"covers": strs,
			})},
			"outOfScope":    strs,
			"risks":         strs,
			"openQuestions": strs,
			"scopeEnvelope": map[string]any{"type": "array", "items": obj([]string{"component", "pathGlobs"}, map[string]any{
				"component": str, "pathGlobs": strs,
			})},
			"estimate": obj([]string{"iterations", "costUsd", "wallclockMinutes"}, map[string]any{
				"iterations": str, "costUsd": map[string]any{"type": "number"}, "wallclockMinutes": map[string]any{"type": "integer"},
			}),
		})
}
