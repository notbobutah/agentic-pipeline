package jira

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/notbobutah/agentic-pipeline/internal/config"
)

// ApplyEnv overrides the jira config with AP_JIRA_* variables, so a cluster
// can configure the watcher from a ConfigMap without changing config.yaml:
//
//	AP_JIRA_SITE, AP_JIRA_PROJECTS (comma-separated), AP_JIRA_TRIGGER_LABEL,
//	AP_JIRA_AGENT_ACCOUNT, AP_JIRA_JQL, AP_JIRA_AC_FIELD, AP_JIRA_POLL_SECONDS,
//	AP_JIRA_IDENTITIES (name=accountId,... for users already in identities)
func ApplyEnv(cfg *config.Config) error {
	j := &cfg.Jira
	set := func(k string, dst *string) {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			*dst = v
		}
	}
	set("AP_JIRA_SITE", &j.Site)
	set("AP_JIRA_TRIGGER_LABEL", &j.TriggerLabel)
	set("AP_JIRA_AGENT_ACCOUNT", &j.AgentAccountID)
	set("AP_JIRA_JQL", &j.JQL)
	set("AP_JIRA_AC_FIELD", &j.AcceptanceCriteriaField)
	if v := os.Getenv("AP_JIRA_PROJECTS"); v != "" {
		j.Projects = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				j.Projects = append(j.Projects, p)
			}
		}
	}
	if v := os.Getenv("AP_JIRA_POLL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("AP_JIRA_POLL_SECONDS: %w", err)
		}
		j.PollSeconds = n
	}
	if v := os.Getenv("AP_JIRA_IDENTITIES"); v != "" {
		for _, pair := range strings.Split(v, ",") {
			name, id, ok := strings.Cut(strings.TrimSpace(pair), "=")
			ident, known := cfg.Identities[name]
			if !ok || !known {
				return fmt.Errorf("AP_JIRA_IDENTITIES: %q must be name=accountId for a user in identities", pair)
			}
			ident.Jira = id
			cfg.Identities[name] = ident
		}
	}
	return nil
}

// Enabled reports whether a Jira site is configured (file or environment).
func Enabled(cfg *config.Config) bool { return cfg.Jira.Site != "" || os.Getenv("AP_JIRA_SITE") != "" }

// FromEnv applies AP_JIRA_* overrides and builds a client. Credentials come
// only from AP_JIRA_EMAIL and AP_JIRA_TOKEN (JIRA_API_KEY is accepted too).
func FromEnv(cfg *config.Config) (*Client, error) {
	if err := ApplyEnv(cfg); err != nil {
		return nil, err
	}
	if cfg.Jira.Site == "" {
		return nil, errors.New("jira.site is not set (config or AP_JIRA_SITE)")
	}
	token := os.Getenv("AP_JIRA_TOKEN")
	if token == "" {
		token = os.Getenv("JIRA_API_KEY")
	}
	c := &Client{Site: cfg.Jira.Site, Email: os.Getenv("AP_JIRA_EMAIL"), Token: token}
	if c.Email == "" || c.Token == "" {
		return nil, errors.New("set AP_JIRA_EMAIL and AP_JIRA_TOKEN (an Atlassian API token)")
	}
	return c, nil
}
