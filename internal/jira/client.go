// Package jira watches Jira Cloud for work assigned to the agent user. It
// stands in for the Jira webhooks of docs/05 §3 by polling the REST API, and
// it emits the same L1 events as the local card file.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls the Jira Cloud REST API v3 with an API token (basic auth).
type Client struct {
	Site  string // https://<name>.atlassian.net
	Email string
	Token string
	HTTP  *http.Client
}

type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

type Named struct {
	Name string `json:"name"`
}

type Fields struct {
	Summary     string          `json:"summary"`
	IssueType   Named           `json:"issuetype"`
	Priority    *Named          `json:"priority"`
	Components  []Named         `json:"components"`
	Labels      []string        `json:"labels"`
	Description json.RawMessage `json:"description"`
	Reporter    *User           `json:"reporter"`
	Assignee    *User           `json:"assignee"`
	// Extra holds every field, for a configured acceptance-criteria custom field.
	Extra map[string]json.RawMessage `json:"-"`
}

type Issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields Fields `json:"fields"`
}

func (i *Issue) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID     string                     `json:"id"`
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	i.ID, i.Key = raw.ID, raw.Key
	if raw.Fields == nil {
		return nil
	}
	fb, _ := json.Marshal(raw.Fields)
	if err := json.Unmarshal(fb, &i.Fields); err != nil {
		return err
	}
	i.Fields.Extra = raw.Fields
	return nil
}

type Comment struct {
	ID     string          `json:"id"`
	Author *User           `json:"author"`
	Body   json.RawMessage `json:"body"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Site, "/")+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Email, c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("jira %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b[:min(len(b), 500)])))
	}
	return json.Unmarshal(b, out)
}

// Search runs a JQL query and returns every matching issue.
func (c *Client) Search(ctx context.Context, jql string, fields []string) ([]Issue, error) {
	var all []Issue
	token := ""
	for {
		req := map[string]any{"jql": jql, "fields": fields, "maxResults": 50}
		if token != "" {
			req["nextPageToken"] = token
		}
		var resp struct {
			Issues        []Issue `json:"issues"`
			NextPageToken string  `json:"nextPageToken"`
			IsLast        bool    `json:"isLast"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", req, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Issues...)
		if resp.IsLast || resp.NextPageToken == "" {
			return all, nil
		}
		token = resp.NextPageToken
	}
}

// Comments returns an issue's comments, oldest first.
func (c *Client) Comments(ctx context.Context, key string) ([]Comment, error) {
	var all []Comment
	for {
		q := url.Values{"startAt": {fmt.Sprint(len(all))}, "maxResults": {"100"}, "orderBy": {"created"}}
		var resp struct {
			Comments []Comment `json:"comments"`
			Total    int       `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Comments...)
		if len(resp.Comments) == 0 || len(all) >= resp.Total {
			return all, nil
		}
	}
}

// Myself returns the account the API token belongs to.
func (c *Client) Myself(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}
