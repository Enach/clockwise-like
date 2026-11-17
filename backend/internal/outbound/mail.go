package outbound

import (
	"errors"
	"strings"
)

// ErrMailNotConfigured is what an email sender returns when SMTP_HOST or
// SMTP_FROM is empty: nothing was sent (PAC-122 SF-066). Before it the
// senders returned nil, so a skipped email looked like a sent one. A caller
// logs it as a skip (with the record id, never the address), not as a
// delivery failure; the startup warning (MailNotices) says email is off.
var ErrMailNotConfigured = errors.New("email not sent: SMTP is not configured (SMTP_HOST or SMTP_FROM is empty)")

// MailConfigured reports whether outbound email is on: SMTP_HOST and
// SMTP_FROM are both set. It is the senders' own test, read by its literal
// names.
func MailConfigured(getenv func(string) string) bool {
	return getenv("SMTP_HOST") != "" && getenv("SMTP_FROM") != ""
}

// MailNotices returns the startup warnings about outbound email (PAC-122
// SF-066). Email off is never fatal: booking confirmations and team
// invitations are then skipped, each skip logged. With email on and
// BASE_URL empty, team invitation links point at https://paceday.app.
func MailNotices(getenv func(string) string) []string {
	if !MailConfigured(getenv) {
		// Each name is read as a literal (the env reference test reads them).
		var missing []string
		if getenv("SMTP_HOST") == "" {
			missing = append(missing, "SMTP_HOST")
		}
		if getenv("SMTP_FROM") == "" {
			missing = append(missing, "SMTP_FROM")
		}
		return []string{"email is off (" + strings.Join(missing, " and ") +
			" not set): booking confirmations and team invitations will not be sent; each skipped email is logged"}
	}
	if getenv("BASE_URL") == "" {
		return []string{"BASE_URL is not set: team invitation emails will link to https://paceday.app"}
	}
	return nil
}
