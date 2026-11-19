package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// AC-18 (PAC-134 contract md §9): docs/runbook.md's "Erasing an address"
// names the command, standard input, the dry run, the account-held case and
// its acknowledgement, the web precondition, every place of spec §3.4, what
// the tool cannot reach, the exit codes, and holds no hand-written DELETE of
// the places any more. PAC-129 AC-27's check (pac129_runbook_test.go) runs
// on the same text.
func TestPAC134_AC18_RunbookErasingAnAddress(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if msg := checkPAC134Runbook(doc); msg != "" {
		t.Fatal(msg)
	}
	if msg := checkErasingAnAddress(doc); msg != "" {
		t.Fatal(msg)
	}
	// The check fails when one required thing goes, or a DELETE comes back.
	for name, broken := range map[string]string{
		"no command":        strings.ReplaceAll(doc, "paceday-operator erase-address", "the tool"),
		"no precondition":   strings.Replace(doc, "**Precondition:**", "Note:", 1),
		"no dry run":        strings.ReplaceAll(doc, "-dry-run", "-try"),
		"no acknowledge":    strings.ReplaceAll(doc, "-account-held-acknowledged", "-ack"),
		"no bookings":       strings.ReplaceAll(doc, "(`bookings`)", "(bookings)"),
		"no cache":          strings.ReplaceAll(doc, "free/busy", "busy"),
		"no exit 3":         strings.ReplaceAll(doc, "3 an account holds", "an account holds"),
		"a DELETE again":    strings.Replace(doc, "The hand-written SQL", "DELETE FROM bookings WHERE x; The hand-written SQL", 1),
		"no standard input": strings.ReplaceAll(doc, "standard input", "stdin"),
	} {
		if checkPAC134Runbook(broken) == "" {
			t.Errorf("%s: the check passed", name)
		}
	}
}

func checkPAC134Runbook(doc string) string {
	const heading = "\n## Erasing an address\n"
	i := strings.Index(doc, heading)
	if i < 0 {
		return `no "## Erasing an address" section`
	}
	section := doc[i+len(heading):]
	if j := strings.Index(section, "\n## "); j >= 0 {
		section = section[:j]
	}
	for _, need := range []string{
		"paceday-operator erase-address", "standard input", "read -rs", "-dry-run", "-account-held-acknowledged",
		"**Precondition:**", "web release that reads erased live bookings", "must be searched by address",
		"`team_added_members`", "`team_invites`", "`manager_team_members`", "`manager_team_member_assignments`",
		"`one_on_one_occurrences`", "`team_analytics_cache`", "`scheduling_link_hosts`", "`scheduling_link_create_keys`",
		"(`bookings`)", "`operator_audit`", "kept_account_own",
		"free text", "free/busy", "15 minutes", "backups", "14-day", "container logs", "Sentry", "providers' calendars",
		"emails already sent", "re-detects", "Exit codes: 0", "1 failed", "2 usage", "3 an account holds",
		"rerun once", "-max-duration", "withdrawn",
	} {
		if !strings.Contains(section, need) {
			return `"Erasing an address" does not mention ` + need
		}
	}
	if m := regexp.MustCompile(`(?i)DELETE\s+FROM\s+(team_added_members|team_invites|manager_team_members|manager_team_member_assignments|one_on_one_occurrences|team_analytics_cache|scheduling_link_hosts|scheduling_link_create_keys|bookings)\b`).FindString(section); m != "" {
		return `"Erasing an address" still holds a hand-written ` + m
	}
	return ""
}
