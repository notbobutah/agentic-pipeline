// Package travelpolicy tests the fixtures in demo/travel-policy.
package travelpolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const fixtures = "../../demo/travel-policy"

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// section returns the text from heading up to the next heading of any level.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(doc, heading+" ")
	if !ok {
		t.Fatalf("no heading %q", heading)
	}
	if loc := regexp.MustCompile(`(?m)^#+ `).FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return squash(rest)
}

// The transcripts quote these sentences, so the wording must not drift.
func TestTranscriptQuotesAppearInCitedSection(t *testing.T) {
	policy, vendors := "corpus/travel-policy-v4.2.md", "corpus/preferred-vendors-guide.md"
	for _, c := range []struct{ file, heading, quote string }{
		{policy, "## §3", "Airfare must be economy class for flights under 6 hours."},
		{policy, "## §4", "Book at least 14 days in advance when possible."},
		{policy, "### §5.1", "Meal per diem is $75/day for domestic travel."},
		{policy, "### §5.2", "Domestic hotel nightly cap is $225."},
		{policy, "## §6", "Rental cars require manager approval."},
		{policy, "## §8", "International trips require a valid passport and economy fare for flights under 6 hours."},
		{vendors, "## Page 2:", "Preferred hotel in Chicago is the Loop Grand, negotiated rate $210/night."},
	} {
		if !strings.Contains(section(t, read(t, c.file), c.heading), c.quote) {
			t.Errorf("%s %s: missing %q", c.file, c.heading, c.quote)
		}
	}
}

func TestVariantsIsolateTheirDefect(t *testing.T) {
	base := read(t, "corpus/travel-policy-v4.2.md")
	noCap := read(t, "variants/travel-policy-v4.2-no-hotel-cap.md")
	for _, s := range []string{"$225", "$180", "nightly cap"} {
		if strings.Contains(noCap, s) {
			t.Errorf("no-hotel-cap variant contains %q", s)
		}
	}
	i, j := strings.Index(base, "### §5.2"), strings.Index(base, "## §6")
	if i < 0 || j < i || base[:i]+base[j:] != noCap {
		t.Error("no-hotel-cap must equal the baseline without §5.2 and §5.3")
	}

	clean := read(t, "corpus/preferred-vendors-guide.md")
	injected := read(t, "variants/preferred-vendors-guide-injected.md")
	for _, s := range []string{"<!--", "SYSTEM NOTICE", auditAddress} {
		if strings.Contains(clean, s) {
			t.Errorf("clean guide contains %q", s)
		}
		if !strings.Contains(injected, s) {
			t.Errorf("injected guide is missing %q", s)
		}
	}
	comment := regexp.MustCompile(`(?s)\n<!--.*?-->\n`)
	if comment.ReplaceAllString(injected, "") != clean {
		t.Error("injected guide must differ from the clean guide only by its comment")
	}
	if !strings.Contains(read(t, "variants/travel-policy-v4.1-superseded.md"), "Status: superseded") {
		t.Error("v4.1 must be marked superseded")
	}
}

// Dates used by the transcripts, checked against TP-BAP §5 (weeks start Monday).
func TestRelativeDates(t *testing.T) {
	today := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	if got := monday.AddDate(0, 0, 7).Format(time.DateOnly); got != "2026-11-30" {
		t.Errorf("next week starts %s", got)
	}
	if got := monday.AddDate(0, 0, 14).Format(time.DateOnly); got != "2026-12-07" {
		t.Errorf("week after next starts %s", got)
	}
	// The golden trip is inside 14 days, so §4 must stay guidance.
	if s := section(t, read(t, "corpus/travel-policy-v4.2.md"), "## §4"); !strings.Contains(s, "does not need an exception") {
		t.Error("§4 must not be mandatory")
	}
}
