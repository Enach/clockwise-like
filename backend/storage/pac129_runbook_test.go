package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC-27 (PAC-129 contract §10): the docs check. docs/runbook.md holds an
// "Erasing an address" section that lists every place of spec §3.11 (every
// table name), says that people with accounts are searched by address, and
// names the follow-up ticket; and the "Applying migrations" step holds the
// migration 041 pre-flight count (P2-m1). It runs in the backend suite, which
// mounts the whole repository (as the PAC-40 runbook check does).
func TestPAC129_AC27_RunbookHasTheErasingAnAddressSection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkErasingAnAddress(strings.ReplaceAll(string(raw), "\r\n", "\n")); err != "" {
		t.Fatal(err)
	}
}

// checkErasingAnAddress returns what is missing from the runbook text, or "".
func checkErasingAnAddress(doc string) string {
	const heading = "\n## Erasing an address\n"
	i := strings.Index(doc, heading)
	if i < 0 {
		return `docs/runbook.md has no "## Erasing an address" section`
	}
	section := doc[i+len(heading):]
	if j := strings.Index(section, "\n## "); j >= 0 {
		section = section[:j]
	}
	for _, need := range []string{
		"team_added_members", "team_invites", "manager_team_member_assignments",
		"one_on_one_occurrences", "team_analytics_cache", "manager_team_members",
		"must be searched by address", "PAC-134",
	} {
		if !strings.Contains(section, need) {
			return `"Erasing an address" does not mention ` + need
		}
	}
	if !strings.Contains(doc, "PAC-129 (migration 041)") || !strings.Contains(doc, "chr(65279)) = ''") {
		return "the migration 041 pre-flight count is missing from Applying migrations"
	}
	return ""
}

// The check fails when the section, a table or the ticket is missing.
func TestPAC129_AC27_DocsCheckFailsWithoutTheSection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for name, broken := range map[string]string{
		"no heading": strings.Replace(doc, "## Erasing an address", "## Erasing", 1),
		// PAC-134 (contract md §9, D-10): the section no longer holds the
		// hand-written DELETEs; the first "team_analytics_cache" of the
		// document is in it, and it names PAC-134 several times, so every
		// occurrence goes.
		"no table":     strings.Replace(doc, "team_analytics_cache", "x", 1),
		"no ticket":    strings.ReplaceAll(doc, "PAC-134", "PAC-0"),
		"no preflight": strings.Replace(doc, "PAC-129 (migration 041)", "PAC-129", 1),
	} {
		if checkErasingAnAddress(broken) == "" {
			t.Errorf("%s: the check passed", name)
		}
	}
}
