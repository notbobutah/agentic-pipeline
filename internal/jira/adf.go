package jira

import (
	"encoding/json"
	"regexp"
	"strings"
)

// node is an Atlassian Document Format node.
type node struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Attrs   json.RawMessage `json:"attrs"`
	Content []node          `json:"content"`
}

func parseADF(raw json.RawMessage) (node, bool) {
	var n node
	if len(raw) == 0 || string(raw) == "null" {
		return n, false
	}
	if json.Unmarshal(raw, &n) != nil || n.Type == "" {
		return n, false
	}
	return n, true
}

// inline flattens a node's text.
func inline(n node) string {
	switch n.Type {
	case "text":
		return n.Text
	case "hardBreak":
		return "\n"
	case "mention", "emoji", "status", "date":
		var a struct {
			Text      string `json:"text"`
			Timestamp string `json:"timestamp"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		return a.Text + a.Timestamp
	case "inlineCard":
		var a struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		return a.URL
	}
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(inline(c))
	}
	return b.String()
}

// blocks renders block nodes as plain text lines; list items get "- ".
func blocks(ns []node, indent string, out *[]string) {
	for _, n := range ns {
		switch n.Type {
		case "bulletList", "orderedList":
			for _, li := range n.Content {
				var sub []string
				blocks(li.Content, "", &sub)
				for i, l := range sub {
					if i == 0 {
						*out = append(*out, indent+"- "+l)
					} else {
						*out = append(*out, indent+"  "+l)
					}
				}
			}
		case "paragraph", "heading", "codeBlock":
			if t := strings.TrimSpace(inline(n)); t != "" {
				*out = append(*out, indent+t)
			}
		case "rule":
		default:
			blocks(n.Content, indent, out)
		}
	}
}

// Text renders an ADF document (or a plain string field) as plain text.
func Text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	doc, ok := parseADF(raw)
	if !ok {
		return ""
	}
	var lines []string
	blocks(doc.Content, "", &lines)
	return strings.Join(lines, "\n")
}

var acHeading = regexp.MustCompile(`(?i)^\s*acceptance\s+criteria\s*:?\s*$`)

// SplitCriteria separates an "Acceptance criteria" section from a description.
// The section starts at a heading or paragraph with that title and ends at the
// next heading; each top-level list item is one criterion.
func SplitCriteria(raw json.RawMessage) (desc string, criteria []string) {
	doc, ok := parseADF(raw)
	if !ok {
		return Text(raw), nil
	}
	var rest []node
	in := false
	for _, n := range doc.Content {
		isTitle := (n.Type == "heading" || n.Type == "paragraph") && acHeading.MatchString(inline(n))
		switch {
		case isTitle:
			in = true
			continue
		case in && n.Type == "heading":
			in = false
		}
		if !in {
			rest = append(rest, n)
			continue
		}
		criteria = append(criteria, listItems(n)...)
	}
	var lines []string
	blocks(rest, "", &lines)
	return strings.Join(lines, "\n"), criteria
}

func listItems(n node) []string {
	if n.Type != "bulletList" && n.Type != "orderedList" {
		if t := strings.TrimSpace(inline(n)); t != "" && n.Type == "paragraph" {
			return []string{t}
		}
		return nil
	}
	var out []string
	for _, li := range n.Content {
		var sub []string
		blocks(li.Content, "", &sub)
		if t := strings.TrimSpace(strings.Join(sub, " ")); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Criteria reads a configured acceptance-criteria field: ADF lists, or plain
// text with one criterion per line.
func Criteria(raw json.RawMessage) []string {
	if doc, ok := parseADF(raw); ok {
		var out []string
		for _, n := range doc.Content {
			out = append(out, listItems(n)...)
		}
		return out
	}
	var out []string
	for _, l := range strings.Split(Text(raw), "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*•")); l != "" {
			out = append(out, l)
		}
	}
	return out
}
