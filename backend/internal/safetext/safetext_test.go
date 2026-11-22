package safetext

import (
	"errors"
	"strings"
	"testing"
)

func TestHasControl(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"Acme Team", false},
		{"José & Co — 1:1", false},
		{"tab\there", false},
		{"Acme\r\nBcc: victim@example.com", true},
		{"Acme\nSubject: other", true},
		{"Acme\rX: y", true},
		{"Acme\x00", true},
	} {
		if got := HasControl(c.in); got != c.want {
			t.Errorf("HasControl(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLabelRemovesEveryLineBreak(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"plain value is untouched", "Acme Team", "Acme Team"},
		{"CRLF becomes one space", "Acme\r\nBcc: victim@example.com", "Acme Bcc: victim@example.com"},
		{"bare LF becomes space", "a\nb", "a b"},
		{"bare CR becomes space", "a\rb", "a b"},
		{"NUL becomes space", "a\x00b", "a b"},
		{"leading and trailing breaks are trimmed", "\r\nAcme\r\n", "Acme"},
	} {
		got := Label(c.in)
		if got != c.want {
			t.Errorf("%s: Label(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if HasControl(got) {
			t.Errorf("%s: Label(%q) still holds a line break: %q", c.name, c.in, got)
		}
	}
}

func TestLabelCapsLength(t *testing.T) {
	got := Label(strings.Repeat("a", MaxLen+50))
	if len(got) != MaxLen {
		t.Fatalf("len = %d, want %d", len(got), MaxLen)
	}
	// Runes, not bytes: a multi-byte value keeps MaxLen characters.
	if got := Label(strings.Repeat("é", MaxLen+50)); len([]rune(got)) != MaxLen {
		t.Fatalf("runes = %d, want %d", len([]rune(got)), MaxLen)
	}
}

func TestLabelIsIdempotent(t *testing.T) {
	for _, in := range []string{"Acme", "a\r\nb", strings.Repeat("x", MaxLen+1), "\r\n", strings.Repeat("é", MaxLen+3)} {
		once := Label(in)
		if twice := Label(once); twice != once {
			t.Errorf("Label(%q): once = %q, twice = %q", in, once, twice)
		}
	}
}

// D-15: an address is an identifier, not prose. A long one is returned whole —
// cutting it to MaxLen runes would name somebody else — and an unusable one is
// an error, so the caller fails the send instead of delivering it elsewhere.
func TestAddressIsNeverTruncated(t *testing.T) {
	long := strings.Repeat("a", MaxAddressLen-len("@example.com")) + "@example.com"
	if len(long) != MaxAddressLen {
		t.Fatalf("test address is %d octets, want %d", len(long), MaxAddressLen)
	}
	if len(long) <= MaxLen {
		t.Fatalf("test address is %d octets: it must be longer than MaxLen (%d) to exercise D-15", len(long), MaxLen)
	}
	got, err := Address(long)
	if err != nil {
		t.Fatalf("a %d-octet address was refused: %v", len(long), err)
	}
	if got != long {
		t.Fatalf("address was changed:\n got %q\nwant %q", got, long)
	}
	if got := Label(long); len(got) == len(long) {
		t.Fatal("Label no longer caps, so this test proves nothing about Address")
	}
}

func TestAddressRefusesWhatItCannotRender(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
	}{
		{"a line break", "booker@example.com\r\nBcc: victim@example.com"},
		{"a bare LF", "booker@example.com\nX: y"},
		{"a NUL", "booker@example.com\x00"},
		{"empty", ""},
		{"only space", "   "},
		{"one octet over the limit", strings.Repeat("a", MaxAddressLen+1-len("@example.com")) + "@example.com"},
	} {
		got, err := Address(c.in)
		if !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("%s: err = %v, want ErrUnsafeAddress", c.name, err)
		}
		if got != "" {
			t.Errorf("%s: returned %q; a refused address must not come back usable", c.name, got)
		}
	}
	// Surrounding space is the one repair: it changes no recipient.
	if got, err := Address("  booker@example.com  "); err != nil || got != "booker@example.com" {
		t.Errorf("Address with surrounding space = %q, %v", got, err)
	}
}
