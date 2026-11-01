package outbound

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
	"golang.org/x/oauth2"
)

func TestDefaultLimitsAreTheSpecTable(t *testing.T) {
	want := Limits{30 * time.Second, 15 * time.Second, 10 * time.Second, 10 * time.Second, 15 * time.Second, 10 << 20, 15 * time.Second}
	if Default != want || Current() != want {
		t.Fatalf("got %+v", Current())
	}
	if ProviderClient().Timeout != 15*time.Second || LLMClient().Timeout != 30*time.Second ||
		IdentityClient().Timeout != 10*time.Second || TokenExchangeClient().Timeout != 10*time.Second ||
		ICSClient().Timeout != 15*time.Second {
		t.Fatal("client timeouts differ from the limits")
	}
}

// AC-A3: a call to a stub that never answers fails within the limit + 1 s,
// independently of the caller's context, and leaves no connection open.
func TestClientFailsWithinLimit(t *testing.T) {
	defer SetForTest(Limits{Provider: 200 * time.Millisecond})()
	h := outboundtest.NewHang(t)
	h.Within(t, 200*time.Millisecond, func() error {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.URL(), nil)
		resp, err := ProviderClient().Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	})
}

func TestIdentityClientContext(t *testing.T) {
	ctx := WithIdentityClient(context.Background())
	c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client)
	if !ok || c.Timeout != Current().Identity {
		t.Fatalf("identity client not on the context: %v", c)
	}
}

func TestReadLimited(t *testing.T) {
	b, err := ReadLimited(strings.NewReader("12345"), 5)
	if err != nil || string(b) != "12345" {
		t.Fatalf("at the cap: %q %v", b, err)
	}
	r := &countingReader{r: strings.NewReader(strings.Repeat("x", 1000))}
	if _, err := ReadLimited(r, 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the cap: %v", err)
	}
	if r.n > 6 {
		t.Fatalf("read %d bytes, want at most cap+1", r.n)
	}
}

type countingReader struct {
	r interface{ Read([]byte) (int, error) }
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if len(p) > 2 {
		p = p[:2]
	}
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// AC-A3 / AC-A8: an SMTP server that accepts and never answers fails the send
// within the SMTP limit + 1 s.
func TestSendMailDeadline(t *testing.T) {
	defer SetForTest(Limits{SMTP: 200 * time.Millisecond})()
	h := outboundtest.NewHang(t)
	h.Within(t, 200*time.Millisecond, func() error {
		return SendMail(h.Addr(), nil, "from@x.test", []string{"to@x.test"}, []byte("hi"))
	})
}

// A cooperative SMTP server receives the message.
func TestSendMailDelivers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go fakeSMTP(ln, got)
	if err := SendMail(ln.Addr().String(), nil, "from@x.test", []string{"to@x.test"}, []byte("Subject: s\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	if m := <-got; !strings.Contains(m, "body") {
		t.Fatalf("message %q", m)
	}
}

func TestSendMailRefused(t *testing.T) {
	if err := SendMail("127.0.0.1:1", nil, "f", []string{"t"}, nil); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
}

// fakeSMTP is a minimal SMTP server for one message.
func fakeSMTP(ln net.Listener, got chan<- string) {
	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	w("220 fake")
	var data strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				inData = false
				w("250 ok")
				got <- data.String()
				continue
			}
			data.WriteString(line)
			continue
		}
		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case cmd == "DATA":
			inData = true
			w("354 go")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestRedactAddresses(t *testing.T) {
	got := RedactAddresses("smtp rcpt: 550 <a@b.test> rejected for c@d.test")
	if strings.Contains(got, "@") || !strings.Contains(got, "550") {
		t.Fatalf("got %q", got)
	}
}
