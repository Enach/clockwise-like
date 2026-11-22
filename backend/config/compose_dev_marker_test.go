package config

import (
	"strings"
	"testing"
)

// Rider of the security audit of 2026-11-22: docker-compose.yml defaults
// POSTGRES_PASSWORD to "clockwise" and repeats it in the backend's
// DATABASE_URL. The default stays — `docker compose up --build` with nothing
// configured is the documented first step of local development — so the file
// has to say what it is for, and the production file must keep requiring the
// value.
func TestDevComposeIsMarkedDevOnly(t *testing.T) {
	dev := readRepoFile(t, "docker-compose.yml")
	head := dev
	if i := strings.Index(dev, "services:"); i > 0 {
		head = dev[:i]
	}
	for _, want := range []string{"LOCAL DEVELOPMENT ONLY", "docker-compose.prod.yml"} {
		if !strings.Contains(head, want) {
			t.Errorf("docker-compose.yml's header does not say %q", want)
		}
	}
	if !strings.Contains(dev, "${POSTGRES_PASSWORD:-clockwise}") {
		t.Error("docker-compose.yml no longer carries the dev default this test is about; update or delete it")
	}
	// A dev default is only harmless while the database is not published.
	if svc := composeServices(t, uncommented(dev))["postgres"]; strings.Contains(svc, "ports:") {
		t.Error("docker-compose.yml publishes the database port with a defaulted password")
	}
}

// The production file has no default for any password: a deployment that
// forgot one does not start.
func TestProdComposeRequiresThePassword(t *testing.T) {
	prod := uncommented(readRepoFile(t, "docker-compose.prod.yml"))
	if !strings.Contains(prod, "${POSTGRES_PASSWORD}") {
		t.Fatal("docker-compose.prod.yml does not take POSTGRES_PASSWORD from the environment")
	}
	for _, bad := range []string{"${POSTGRES_PASSWORD:-", "${POSTGRES_PASSWORD-"} {
		if strings.Contains(prod, bad) {
			t.Errorf("docker-compose.prod.yml defaults the database password (%s…)", bad)
		}
	}
	if strings.Contains(prod, "clockwise}") {
		t.Error("docker-compose.prod.yml carries a literal password default")
	}
}
