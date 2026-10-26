package nlp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/config"
)

type countingTransport struct{ n int }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
}

// The transport is the last line: whatever built the URL, an unapproved
// destination is refused at the moment of sending (spec §3).
func TestApprovedOnlyTransport_RefusesAtTheMomentOfSending(t *testing.T) {
	inner := &countingTransport{}
	t.Cleanup(SetApprovedHosts(config.LLMApprovedHosts{ProviderVertex: {"europe-west4-aiplatform.googleapis.com": true}}))
	t.Cleanup(SetAmbientDeps(AmbientDeps{Transport: inner}))
	c := ambientHTTPClient(ProviderVertex)
	send := func(target, host string) error {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	for _, target := range []string{
		"https://attacker.example/x",
		"http://europe-west4-aiplatform.googleapis.com/x",
		"https://europe-west4-aiplatform.googleapis.com:8443/x",
		"https://u@europe-west4-aiplatform.googleapis.com/x",
	} {
		var r *DestinationRefusal
		if err := send(target, ""); !errors.As(err, &r) {
			t.Errorf("%s: err = %v, want a refusal", target, err)
		}
	}
	// A Host header that differs from the URL's host is refused too.
	if err := send("https://europe-west4-aiplatform.googleapis.com/x", "attacker.example"); err == nil {
		t.Error("a mismatched Host header was sent")
	}
	if inner.n != 0 {
		t.Fatalf("%d requests reached the network, want zero", inner.n)
	}

	if err := send("https://Europe-West4-aiplatform.googleapis.com/x", ""); err != nil || inner.n != 1 {
		t.Fatalf("approved request: err %v, sent %d, want sent once", err, inner.n)
	}

	// A transport refusal is attributed to the provider the client was made for.
	t.Cleanup(SetApprovedHosts(config.LLMApprovedHosts{ProviderAzureOpenAI: {"europe-west4-aiplatform.googleapis.com": true}}))
	if err := send("https://europe-west4-aiplatform.googleapis.com/x", ""); err == nil || inner.n != 1 {
		t.Errorf("a host approved only for azure_openai was sent the vertex credential (D-8)")
	}
}

// Spec shared Given: a refusal is reported even when the credential cannot be
// acquired, and an approved destination with no credential is the provider
// failure it always was.
func TestRefusalDoesNotDependOnTheCredential(t *testing.T) {
	inner := &countingTransport{}
	t.Cleanup(SetApprovedHosts(config.LLMApprovedHosts{ProviderAzureOpenAI: {"my-resource.openai.azure.com": true}}))
	noCred := func(context.Context) (string, error) {
		return "", errors.New("azure: credential: none in this environment")
	}
	t.Cleanup(SetAmbientDeps(AmbientDeps{Transport: inner, AzureToken: noCred, VertexToken: noCred}))

	_, err := (&AzureOpenAIClient{Endpoint: "https://attacker.example"}).Complete(context.Background(), "s", "u")
	var r *DestinationRefusal
	if !errors.As(err, &r) || r.Reason != ReasonDestinationNotApproved {
		t.Errorf("unapproved, no credential: err = %v, want the destination refusal", err)
	}
	_, err = (&AzureOpenAIClient{Endpoint: "https://my-resource.openai.azure.com"}).Complete(context.Background(), "s", "u")
	if err == nil || errors.As(err, &r) || !strings.Contains(err.Error(), "azure: credential") {
		t.Errorf("approved, no credential: err = %v, want the credential error", err)
	}
	if inner.n != 0 {
		t.Errorf("%d requests sent, want zero", inner.n)
	}
}

// Before main installs a policy, nothing is approved (fail closed, D-1).
func TestNoPolicyApprovesNothing(t *testing.T) {
	t.Cleanup(SetApprovedHosts(nil))
	for _, c := range []LLMClient{
		&AzureOpenAIClient{Endpoint: "https://my-resource.openai.azure.com"},
		&VertexClient{Location: "us-central1"},
		&BedrockClient{Region: "us-east-1"},
	} {
		var r *DestinationRefusal
		if err := CheckDestination(context.Background(), c); !errors.As(err, &r) || r.Reason != ReasonProviderNotEnabled {
			t.Errorf("%T: err = %v, want provider_not_enabled", c, err)
		}
	}
}
