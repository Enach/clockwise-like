package config

import (
	"strings"
	"testing"
)

// PAC-50 spec §4.1: the identity-provider addresses the admin step-up uses
// can be redirected to a stand-in only under the PAC-43 test-mode declaration.

func TestDecideIdP_ProductionByDefault(t *testing.T) {
	g, err := DecideGoogleIdP("", "")
	if err != nil {
		t.Fatal(err)
	}
	if g != ProductionGoogleIdP() || g.Guard || g.Notice != "" {
		t.Fatalf("google = %+v", g)
	}
	if g.AuthURL != "https://accounts.google.com/o/oauth2/v2/auth" ||
		g.TokenURL != "https://oauth2.googleapis.com/token" ||
		g.JWKSURL != "https://www.googleapis.com/oauth2/v3/certs" ||
		g.Issuer != "https://accounts.google.com" ||
		g.APIBase != "https://admin.googleapis.com/admin/directory/v1" {
		t.Fatalf("google production endpoints = %+v", g)
	}

	m, err := DecideMicrosoftIdP("", "true")
	if err != nil {
		t.Fatal(err)
	}
	if m != ProductionMicrosoftIdP() || m.Guard {
		t.Fatalf("microsoft = %+v", m)
	}
	if m.Issuer != "https://login.microsoftonline.com/{tenantid}/v2.0" || m.APIBase != "https://graph.microsoft.com/v1.0" {
		t.Fatalf("microsoft production endpoints = %+v", m)
	}
}

func TestDecideIdP_StandinNeedsDeclaration(t *testing.T) {
	for _, allow := range []string{"", "false", "yes", "0"} {
		if _, err := DecideGoogleIdP("http://fake-google:8080", allow); err == nil || !strings.Contains(err.Error(), EnvAllowStandins) {
			t.Errorf("google allow=%q: err = %v, want a refusal naming %s", allow, err, EnvAllowStandins)
		}
		if _, err := DecideMicrosoftIdP("http://fake-ms:8080", allow); err == nil || !strings.Contains(err.Error(), EnvMicrosoftIdPBaseURL) {
			t.Errorf("microsoft allow=%q: err = %v", allow, err)
		}
	}
}

func TestDecideIdP_StandinDeclared(t *testing.T) {
	g, err := DecideGoogleIdP("http://fake-google:8080/", "true")
	if err != nil {
		t.Fatal(err)
	}
	want := GoogleIdPAt("http://fake-google:8080")
	want.Guard = true
	want.Notice = g.Notice
	if g != want || g.Notice == "" {
		t.Fatalf("google = %+v, want %+v", g, want)
	}
	if g.TokenURL != "http://fake-google:8080/token" || g.Issuer != "http://fake-google:8080" {
		t.Fatalf("google stand-in endpoints = %+v", g)
	}

	m, err := DecideMicrosoftIdP("http://10.0.0.5:9000", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Guard || m.Issuer != "http://10.0.0.5:9000/{tenantid}/v2.0" || m.APIBase != "http://10.0.0.5:9000/v1.0" {
		t.Fatalf("microsoft stand-in = %+v", m)
	}
}

func TestDecideIdP_RefusesBadAddresses(t *testing.T) {
	for _, base := range []string{
		"://nope",
		"ftp://fake:21",
		"http://",
		"http://user:pw@fake:80",
		"http://8.8.8.8:80",
		"http://[fe80::1%25eth0]:80",
	} {
		if _, err := DecideGoogleIdP(base, "true"); err == nil {
			t.Errorf("google %q accepted", base)
		} else if strings.Contains(err.Error(), "pw") {
			t.Errorf("google %q: the password was echoed: %v", base, err)
		}
	}
}
