// Package outbound holds the per-call limits of every outbound dependency
// (PAC-81 Part A, AC-A3; spec section 3 and D-4) and the clients that
// enforce them. A limit is enforced by the client itself, so a call ends
// within it whatever context the caller passes, and the connection of a
// call that timed out is closed.
package outbound

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Limits are the per-call bounds.
type Limits struct {
	LLM           time.Duration // each LLM call
	Provider      time.Duration // calendar, conferencing, Slack and Notion API calls
	Identity      time.Duration // Google/Microsoft exchange and userinfo, OIDC discovery/token/JWKS, renewals
	TokenExchange time.Duration // Slack and Notion OAuth token exchange
	ICS           time.Duration // ICS feed fetch
	ICSMaxBytes   int64         // ICS body cap
	SMTP          time.Duration // SMTP send, dial to QUIT
}

// Default is spec section 3's table.
var Default = Limits{
	LLM:           30 * time.Second,
	Provider:      15 * time.Second,
	Identity:      10 * time.Second,
	TokenExchange: 10 * time.Second,
	ICS:           15 * time.Second,
	ICSMaxBytes:   10 << 20,
	SMTP:          15 * time.Second,
}

var (
	mu      sync.RWMutex
	current = Default
)

// Current returns the limits in force.
func Current() Limits {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// SetForTest replaces the limits and returns a restore function. Tests use
// it to scale the bounds down; production never calls it.
func SetForTest(l Limits) (restore func()) {
	mu.Lock()
	prev := current
	current = l
	mu.Unlock()
	return func() {
		mu.Lock()
		current = prev
		mu.Unlock()
	}
}

// transport is shared by every client of this package (connection reuse);
// a timed-out request closes its own connection.
var transport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	return t
}()

// Client returns a client whose every call is bounded by limit.
func Client(limit time.Duration) *http.Client {
	return &http.Client{Timeout: limit, Transport: transport}
}

// LLMClient, ProviderClient, IdentityClient, TokenExchangeClient and
// ICSClient return clients bounded by the current limits.
func LLMClient() *http.Client           { return Client(Current().LLM) }
func ProviderClient() *http.Client      { return Client(Current().Provider) }
func IdentityClient() *http.Client      { return Client(Current().Identity) }
func TokenExchangeClient() *http.Client { return Client(Current().TokenExchange) }
func ICSClient() *http.Client           { return Client(Current().ICS) }

// WithIdentityClient makes golang.org/x/oauth2 (exchange, renewal) and
// go-oidc (discovery, JWKS) use the identity-bounded client. A client the
// caller already put on ctx (the step-up client's 15 s) is kept.
func WithIdentityClient(ctx context.Context) context.Context {
	if _, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, IdentityClient())
}

// ErrTooLarge is returned when a body exceeds its cap.
var ErrTooLarge = errors.New("response body exceeds its size limit")

// ReadLimited reads r up to max bytes. A body longer than max fails with
// ErrTooLarge after reading at most max+1 bytes.
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, max)
	}
	return b, nil
}

// SendMail is smtp.SendMail with one deadline, the SMTP limit, from the dial
// to QUIT (net/smtp has none). TLS is negotiated with STARTTLS when the
// server offers it, as smtp.SendMail does.
func SendMail(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	limit := Current().SMTP
	deadline := time.Now().Add(limit)
	conn, err := net.DialTimeout("tcp", addr, limit)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if a != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(a); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data end: %w", err)
	}
	if err := c.Quit(); err != nil && !strings.Contains(err.Error(), "closed") {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

// RedactAddresses replaces every word containing "@" with "<address>", so an
// SMTP error quoting a recipient can be logged (PAC-81 AC-A7/A8: log lines
// never carry an email address).
func RedactAddresses(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' })
	for _, f := range fields {
		if strings.Contains(f, "@") {
			s = strings.ReplaceAll(s, f, "<address>")
		}
	}
	return s
}
