package main

// PAC-97 (spec docs/specs/PAC-97.md D-9, AC-27): the DNS stand-in for
// sign-in detection. The backend sends its MX queries here
// (PACEDAY_MX_RESOLVER, honoured only with PACEDAY_ALLOW_STANDINS), so a
// journey that types a company address never queries the real internet.
//
// The answers are fixed by name, so a test needs no control plane and every
// test can use its own random subdomain (detection caches per domain):
//
//	<anything>.google-mx.pac97.test    Google Workspace MX, the gorgias.com shape -> google
//	<anything>.gateway-mx.pac97.test   a mail gateway (the mimecast.test shape)   -> choose
//	<anything>.microsoft-mx.pac97.test Exchange Online MX                         -> microsoft
//	anything else                      NXDOMAIN                                   -> choose

import (
	"log"
	"net"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

type mxRecord struct {
	pref uint16
	host string
}

// mxFixtures maps a zone suffix (lower case, fully qualified) to its MX set.
var mxFixtures = []struct {
	suffix  string
	records []mxRecord
}{
	{".google-mx.pac97.test.", []mxRecord{
		{1, "aspmx.l.google.com."}, {5, "alt1.aspmx.l.google.com."}, {5, "alt2.aspmx.l.google.com."},
		{10, "alt3.aspmx.l.google.com."}, {10, "alt4.aspmx.l.google.com."},
	}},
	{".gateway-mx.pac97.test.", []mxRecord{
		{10, "eu-smtp-inbound-1.mimecast.com."}, {10, "eu-smtp-inbound-2.mimecast.com."},
	}},
	{".microsoft-mx.pac97.test.", []mxRecord{
		{0, "pac97-test.mail.protection.outlook.com."},
	}},
}

// mxAnswer returns the MX set for a fully-qualified name, or false for
// NXDOMAIN.
func mxAnswer(name string) ([]mxRecord, bool) {
	name = strings.ToLower(name)
	for _, f := range mxFixtures {
		if strings.HasSuffix(name, f.suffix) && len(name) > len(f.suffix) {
			return f.records, true
		}
	}
	return nil, false
}

// dnsReply builds the answer to one query, or nil for a packet that is not
// a parseable query.
func dnsReply(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil || h.Response {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	records, found := mxAnswer(q.Name.String())
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true}
	if !found {
		rh.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil
	}
	if err := b.Question(q); err != nil {
		return nil
	}
	if err := b.StartAnswers(); err != nil {
		return nil
	}
	if found && q.Type == dnsmessage.TypeMX && q.Class == dnsmessage.ClassINET {
		for _, r := range records {
			host, err := dnsmessage.NewName(r.host)
			if err != nil {
				return nil
			}
			if err := b.MXResource(
				dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60},
				dnsmessage.MXResource{Pref: r.pref, MX: host},
			); err != nil {
				return nil
			}
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// serveDNS answers MX queries over UDP on addr until the socket fails.
func serveDNS(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	log.Printf("fake-google DNS stand-in listening on udp %s (MX fixtures under pac97.test)", addr)
	return serveDNSOn(pc)
}

// serveDNSOn answers MX queries on an open socket until it fails.
func serveDNSOn(pc net.PacketConn) error {
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		if out := dnsReply(buf[:n]); out != nil {
			_, _ = pc.WriteTo(out, from)
		}
	}
}
