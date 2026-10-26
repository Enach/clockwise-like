package nlp_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-66 review finding on #214: the approved-only transport guards the
// inference request only. The AWS default credential chain (web identity via
// STS, assume-role, ECS, IMDS, SSO) must still reach its own endpoints, or
// Bedrock with an instance or workload role cannot send even to an approved
// host (spec §1 names instance roles). These tests run the real credential
// chain: AWSCredentials is nil, and a local fake stands in for STS or IMDS.

const (
	roleAccessKey = "AKIAROLEPAC66XXXXXXX"
	roleSecretKey = "role-secret-pac66"
	roleToken     = "role-session-token-pac66"
)

// credentialFake is a local STS or IMDS stand-in that records the paths hit.
type credentialFake struct {
	mu    sync.Mutex
	paths []string
}

func (f *credentialFake) hit(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
}

func (f *credentialFake) hits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// realCredentialChain keeps nlptest.Ambient's approved hosts and recording
// transport but drops the injected AWS credential, so the SDK's default chain
// runs.
func realCredentialChain(t *testing.T, rec *nlptest.Recorder) {
	t.Helper()
	t.Cleanup(nlp.SetAmbientDeps(nlp.AmbientDeps{Transport: rec}))
}

func signedByRole(t *testing.T, req nlptest.Request) {
	t.Helper()
	if !strings.Contains(req.Header.Get("Authorization"), roleAccessKey) {
		t.Errorf("Authorization = %q, want a SigV4 signature by the role's key", req.Header.Get("Authorization"))
	}
	if req.Header.Get("X-Amz-Security-Token") != roleToken {
		t.Errorf("X-Amz-Security-Token = %q, want the role's session token", req.Header.Get("X-Amz-Security-Token"))
	}
}

func TestBedrock_WebIdentityRoleReachesSTSAndSendsToApprovedHost(t *testing.T) { // spec §1, review #214
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	realCredentialChain(t, rec)

	fake := &credentialFake{}
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.hit(r)
		_ = r.ParseForm()
		if r.Form.Get("Action") != "AssumeRoleWithWebIdentity" {
			http.Error(w, "unexpected action "+r.Form.Get("Action"), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
			`<AssumeRoleWithWebIdentityResult><Credentials>` +
			`<AccessKeyId>` + roleAccessKey + `</AccessKeyId><SecretAccessKey>` + roleSecretKey + `</SecretAccessKey>` +
			`<SessionToken>` + roleToken + `</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration>` +
			`</Credentials><AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/paceday/s</Arn>` +
			`<AssumedRoleId>AROAPAC66:s</AssumedRoleId></AssumedRoleUser></AssumeRoleWithWebIdentityResult>` +
			`<ResponseMetadata><RequestId>pac66</RequestId></ResponseMetadata></AssumeRoleWithWebIdentityResponse>`))
	}))
	t.Cleanup(sts.Close)

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("web-identity-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/paceday")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", tokenFile)
	t.Setenv("AWS_ROLE_SESSION_NAME", "pac66")
	t.Setenv("AWS_ENDPOINT_URL_STS", sts.URL)

	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "bedrock", AWSRegion: "us-east-1"}), bedrockUS)
	signedByRole(t, req)
	if got := fake.hits(); len(got) != 1 {
		t.Errorf("STS fake hits = %v, want exactly one AssumeRoleWithWebIdentity", got)
	}
}

func TestBedrock_InstanceRoleReachesIMDSAndSendsToApprovedHost(t *testing.T) { // spec §1, review #214
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	realCredentialChain(t, rec)

	fake := &credentialFake{}
	imds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.hit(r)
		p := strings.Replace(r.URL.Path, "security-credentials-extended", "security-credentials", 1)
		switch {
		case r.Method == http.MethodPut && p == "/latest/api/token":
			w.Header().Set("X-Aws-Ec2-Metadata-Token-Ttl-Seconds", "21600")
			_, _ = w.Write([]byte("imds-session-token"))
		case p == "/latest/meta-data/iam/security-credentials/":
			_, _ = w.Write([]byte("paceday"))
		case p == "/latest/meta-data/iam/security-credentials/paceday":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Code":"Success","Type":"AWS-HMAC","AccessKeyId":"` + roleAccessKey +
				`","SecretAccessKey":"` + roleSecretKey + `","Token":"` + roleToken +
				`","Expiration":"2099-01-01T00:00:00Z","LastUpdated":"2026-01-01T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(imds.Close)

	t.Setenv("AWS_EC2_METADATA_DISABLED", "false")
	t.Setenv("AWS_EC2_METADATA_SERVICE_ENDPOINT", imds.URL+"/")

	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "bedrock", AWSRegion: "us-east-1"}), bedrockUS)
	signedByRole(t, req)
	if len(fake.hits()) == 0 {
		t.Error("IMDS fake was never asked for the instance role's credentials")
	}
}
