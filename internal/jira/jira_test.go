package jira

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

const agentID, robID = "acc-agent", "acc-rob"

func adf(blocks ...string) string {
	return `{"type":"doc","version":1,"content":[` + strings.Join(blocks, ",") + `]}`
}
func para(t string) string {
	return `{"type":"paragraph","content":[{"type":"text","text":"` + t + `"}]}`
}
func heading(t string) string {
	return `{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"` + t + `"}]}`
}
func bullets(items ...string) string {
	var li []string
	for _, it := range items {
		li = append(li, `{"type":"listItem","content":[`+para(it)+`]}`)
	}
	return `{"type":"bulletList","content":[` + strings.Join(li, ",") + `]}`
}

func issue(key, reporter string) string {
	return `{"id":"1000` + key[len(key)-1:] + `","key":"` + key + `","fields":{
		"summary":"Add a footer","issuetype":{"name":"Story"},"priority":{"name":"High"},
		"components":[{"name":"Web"}],"labels":[],
		"reporter":{"accountId":"` + reporter + `","displayName":"Someone"},
		"assignee":{"accountId":"` + agentID + `","displayName":"pipeline-agent"},
		"description":` + adf(para("Show a footer on every page."), heading("Acceptance criteria"), bullets("AC1: footer shows the year", "AC2: footer links to /about"), heading("Notes"), para("Keep it small.")) + `}}`
}

type fake struct {
	mu       sync.Mutex
	jql      []string
	issues   []string
	comments map[string][]string
	auth     bool
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != "me@example.com" || p != "tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.auth = true
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
		var req struct{ JQL string }
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		f.jql = append(f.jql, req.JQL)
		io.WriteString(w, `{"isLast":true,"issues":[`+strings.Join(f.issues, ",")+`]}`)
	case strings.HasSuffix(r.URL.Path, "/comment"):
		key := strings.Split(r.URL.Path, "/")[5]
		cs := f.comments[key]
		io.WriteString(w, `{"startAt":0,"total":`+itoa(len(cs))+`,"comments":[`+strings.Join(cs, ",")+`]}`)
	default:
		http.NotFound(w, r)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func comment(id, author, text string) string {
	return `{"id":"` + id + `","author":{"accountId":"` + author + `","displayName":"X"},"body":` + adf(para(text)) + `}`
}

func setup(t *testing.T, f *fake) (*Watcher, *event.Store) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cfg, err := config.Load(filepath.Join(root, "demo/config.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Identities = map[string]config.Identity{"rob": {GitHub: "notbobutah", Jira: robID}}
	cfg.Jira = config.Jira{AgentAccountID: agentID, Projects: []string{"SCRUM"}}
	cat, err := event.LoadCatalog(filepath.Join(root, "demo/catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := event.Open(filepath.Join(t.TempDir(), "events.db"), cat)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Watcher{Client: &Client{Site: srv.URL, Email: "me@example.com", Token: "tok"}, Store: st, Cfg: cfg, Log: slog.New(slog.DiscardHandler)}, st
}

func TestPollSubmitsNewIssuesOnce(t *testing.T) {
	f := &fake{issues: []string{issue("SCRUM-1", robID)}, comments: map[string][]string{"SCRUM-1": {
		comment("1", robID, "Use the existing Tailwind styles."),
		comment("2", agentID, "Plan posted."), // the agent's own comment: skipped
		comment("3", "acc-other", "/ap approve"),
	}}}
	w, st := setup(t, f)
	ctx := context.Background()

	n, err := w.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 { // assigned + 2 comments
		t.Fatalf("first poll published %d events, want 3", n)
	}
	if !f.auth {
		t.Fatal("no authenticated request")
	}
	if want := `project in ("SCRUM") AND assignee = "acc-agent" AND statusCategory != Done ORDER BY created ASC`; f.jql[0] != want {
		t.Errorf("jql = %s", f.jql[0])
	}
	if n, err := w.Once(ctx); err != nil || n != 0 {
		t.Fatalf("second poll published %d events (err %v), want 0", n, err)
	}

	state, err := work.Load(ctx, st, w.Cfg.Tenant, "SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	c := state.Card
	if c.Sponsor != "rob" || c.IssueType != "Story" || c.Priority != "High" || strings.Join(c.Components, ",") != "web" {
		t.Errorf("card = %+v", c)
	}
	if strings.Join(c.AcceptanceCriteria, "|") != "AC1: footer shows the year|AC2: footer links to /about" {
		t.Errorf("criteria = %q", c.AcceptanceCriteria)
	}
	if c.Description != "Show a footer on every page.\nNotes\nKeep it small." {
		t.Errorf("description = %q", c.Description)
	}

	evs, err := st.Correlation(ctx, work.CorrelationID(w.Cfg.Tenant, "SCRUM-1"))
	if err != nil {
		t.Fatal(err)
	}
	var authors []string
	for _, e := range evs {
		if e.Source != "//tracker/jira" {
			t.Errorf("source = %s", e.Source)
		}
		if e.Type == work.TItemCommented {
			var cm work.Commented
			_ = e.Decode(&cm)
			authors = append(authors, cm.Author+":"+cm.CommentID)
		}
	}
	if strings.Join(authors, ",") != "rob:jira-1,jira:X:jira-3" {
		t.Errorf("comment authors = %v", authors)
	}

	// A comment added later is picked up on the next poll.
	f.mu.Lock()
	f.comments["SCRUM-1"] = append(f.comments["SCRUM-1"], comment("4", robID, "/ap approve"))
	f.mu.Unlock()
	if n, _ := w.Once(ctx); n != 1 {
		t.Errorf("poll after new comment published %d, want 1", n)
	}
}

func TestUnmappedReporterIsFlagged(t *testing.T) {
	f := &fake{issues: []string{issue("SCRUM-2", "acc-stranger")}, comments: map[string][]string{}}
	w, _ := setup(t, f)
	issues, err := w.Client.Search(context.Background(), w.JQL(), w.fields())
	if err != nil || len(issues) != 1 {
		t.Fatal(err, len(issues))
	}
	c := w.Card(&issues[0])
	if c.Sponsor != "jira:Someone" {
		t.Errorf("sponsor = %s", c.Sponsor)
	}
	if p := w.Problems(c); len(p) != 1 || !strings.Contains(p[0], "reporter") {
		t.Errorf("problems = %v", p)
	}
}

func TestCriteriaField(t *testing.T) {
	if got := Criteria(json.RawMessage(`"- one\n* two\n\nthree"`)); strings.Join(got, "|") != "one|two|three" {
		t.Errorf("plain text: %q", got)
	}
	if got := Criteria(json.RawMessage(adf(bullets("a", "b")))); strings.Join(got, "|") != "a|b" {
		t.Errorf("adf: %q", got)
	}
}

func TestBadCredentials(t *testing.T) {
	w, _ := setup(t, &fake{})
	w.Client.Token = "wrong"
	if _, err := w.Once(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestLabelTrigger(t *testing.T) {
	labelled := strings.Replace(issue("KAN-7", robID), `"labels":[]`, `"labels":["ap-agent","Web","data","frontend"]`, 1)
	labelled = strings.Replace(labelled, `"components":[{"name":"Web"}]`, `"components":[]`, 1)
	unlabelled := strings.Replace(issue("KAN-8", robID), `"components":[{"name":"Web"}]`, `"components":[]`, 1)
	f := &fake{issues: []string{labelled, unlabelled}, comments: map[string][]string{
		"KAN-7": {comment("9", robID, "/ap approve")}, // no agent account set: the user's own comments count
	}}
	w, st := setup(t, f)
	w.Cfg.Jira = config.Jira{TriggerLabel: "ap-agent", Projects: []string{"KAN"}}
	if want := `project in ("KAN") AND labels = "ap-agent" AND statusCategory != Done ORDER BY created ASC`; w.JQL() != want {
		t.Errorf("jql = %s", w.JQL())
	}
	n, err := w.Once(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("published %d (err %v), want 2: KAN-7 and its comment, not KAN-8", n, err)
	}
	state, _ := work.Load(context.Background(), st, w.Cfg.Tenant, "KAN-7")
	if got := strings.Join(state.Card.Components, ","); got != "web,data" {
		t.Errorf("components = %s (labels that are not components must be ignored)", got)
	}
}

func TestApplyEnv(t *testing.T) {
	w, _ := setup(t, &fake{})
	t.Setenv("AP_JIRA_SITE", "https://x.atlassian.net")
	t.Setenv("AP_JIRA_PROJECTS", "KAN, OPS")
	t.Setenv("AP_JIRA_TRIGGER_LABEL", "ap-agent")
	t.Setenv("AP_JIRA_POLL_SECONDS", "45")
	t.Setenv("AP_JIRA_IDENTITIES", "rob=acc-new")
	if err := ApplyEnv(w.Cfg); err != nil {
		t.Fatal(err)
	}
	j := w.Cfg.Jira
	if j.Site != "https://x.atlassian.net" || strings.Join(j.Projects, "|") != "KAN|OPS" || j.TriggerLabel != "ap-agent" || j.PollSeconds != 45 {
		t.Errorf("jira = %+v", j)
	}
	if w.Cfg.Identities["rob"].Jira != "acc-new" || w.Cfg.Identities["rob"].GitHub != "notbobutah" {
		t.Errorf("identity = %+v", w.Cfg.Identities["rob"])
	}
	t.Setenv("AP_JIRA_IDENTITIES", "mallory=acc-x")
	if err := ApplyEnv(w.Cfg); err == nil {
		t.Error("an unknown identity must be an error, not a new approver")
	}
}
