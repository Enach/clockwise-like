package engine

import (
	"regexp"
	"strings"
	"testing"
)

// Read-only security audit, finding 2 (MEDIUM, unauthenticated): a default
// slug used to be exactly "{first-name}-{duration}min", so an unauthenticated
// first-name dictionary sweep of GET /api/book/{slug} harvested every accepted
// host's email, name, avatar and calendar coverage. Generated slugs now carry
// 32 bits of entropy. Generation only: nothing rewrites a stored slug, and
// every old slug keeps resolving.
//
// And the rider: GenerateSlug indexed Fields(ownerName)[0] without checking
// it existed, so an account whose IdP sent no name (OIDC `name` is optional,
// handlers_sso.go passes userInfo.Name straight through) panicked — a 500 on
// every POST /api/scheduling-links without an explicit slug.

var generatedSlug = regexp.MustCompile(`^[a-z0-9-]+-[0-9]+min-[0-9a-f]{8}$`)

func TestSlugBaseWithoutAUsableName(t *testing.T) {
	for _, c := range []struct {
		name      string
		ownerName string
		want      string
	}{
		{"an empty name", "", defaultSlugStem + "-30min"},
		{"whitespace only", "   \t ", defaultSlugStem + "-30min"},
		{"nothing slugifiable", "李雷", defaultSlugStem + "-30min"},
		{"a first name", "Nicolas Hochart", "nicolas-30min"},
		{"punctuation in the first name", "O'Brien Smith", "obrien-30min"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := slugBase(c.ownerName, 30); got != c.want {
				t.Fatalf("slugBase(%q, 30) = %q, want %q", c.ownerName, got, c.want)
			}
		})
	}
}

func TestGenerateSlugIsNotGuessableFromTheOwnerName(t *testing.T) {
	e := &BookingEngine{DB: openTestDB(t)}

	first, err := e.GenerateSlug("Nicolas Hochart", 30)
	if err != nil {
		t.Fatalf("GenerateSlug: %v", err)
	}
	if first == "nicolas-30min" {
		t.Fatalf("slug = %q: the default slug is still the guessable {first}-{duration}min", first)
	}
	if !strings.HasPrefix(first, "nicolas-30min-") || !generatedSlug.MatchString(first) {
		t.Fatalf("slug = %q, want nicolas-30min- plus 8 hex characters", first)
	}

	// Two links of the same owner and duration never share a slug, and the
	// suffix is not a counter.
	seen := map[string]bool{first: true}
	for range 20 {
		s, err := e.GenerateSlug("Nicolas Hochart", 30)
		if err != nil {
			t.Fatalf("GenerateSlug: %v", err)
		}
		if seen[s] {
			t.Fatalf("slug %q generated twice in 21 draws: the suffix is not random", s)
		}
		seen[s] = true
	}
}

// An account whose provider sent no name still gets a slug, not a panic.
func TestGenerateSlugWithoutAnOwnerName(t *testing.T) {
	e := &BookingEngine{DB: openTestDB(t)}
	got, err := e.GenerateSlug("", 45)
	if err != nil {
		t.Fatalf("GenerateSlug(\"\"): %v", err)
	}
	if !strings.HasPrefix(got, defaultSlugStem+"-45min-") || !generatedSlug.MatchString(got) {
		t.Fatalf("slug = %q, want %s-45min- plus 8 hex characters", got, defaultSlugStem)
	}
}
