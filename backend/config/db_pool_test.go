package config

import (
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDecideDBPoolDefaults(t *testing.T) {
	p, n := DecideDBPool(envOf(nil))
	if p != (DBPool{20, 10, 30 * time.Minute, 5 * time.Minute}) || len(n) != 0 {
		t.Fatalf("got %+v %v", p, n)
	}
}

func TestDecideDBPoolOverrides(t *testing.T) {
	p, n := DecideDBPool(envOf(map[string]string{
		EnvDBMaxOpenConns: "8", EnvDBMaxIdleConns: "4", EnvDBConnMaxLifetime: "10m", EnvDBConnMaxIdleTime: "90s",
	}))
	if p != (DBPool{8, 4, 10 * time.Minute, 90 * time.Second}) || len(n) != 0 {
		t.Fatalf("got %+v %v", p, n)
	}
}

// AC-A9: a non-positive or unparseable value is ignored with a warning.
func TestDecideDBPoolIgnoresBadValues(t *testing.T) {
	p, n := DecideDBPool(envOf(map[string]string{
		EnvDBMaxOpenConns: "0", EnvDBMaxIdleConns: "lots", EnvDBConnMaxLifetime: "-1m", EnvDBConnMaxIdleTime: "soon",
	}))
	if p != DefaultDBPool {
		t.Fatalf("got %+v", p)
	}
	if len(n) != 4 {
		t.Fatalf("notices %v", n)
	}
	for _, name := range []string{EnvDBMaxOpenConns, EnvDBMaxIdleConns, EnvDBConnMaxLifetime, EnvDBConnMaxIdleTime} {
		found := false
		for _, s := range n {
			found = found || strings.Contains(s, name)
		}
		if !found {
			t.Fatalf("no notice names %s: %v", name, n)
		}
	}
}

func TestDecideDBPoolIdleNotAboveOpen(t *testing.T) {
	p, _ := DecideDBPool(envOf(map[string]string{EnvDBMaxOpenConns: "1"}))
	if p.MaxOpen != 1 || p.MaxIdle != 1 {
		t.Fatalf("got %+v", p)
	}
}
