package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// failingSaveStore holds an expired credential and cannot store a renewal.
type failingSaveStore struct{ tok *oauth2.Token }

func (f failingSaveStore) load(context.Context) (*oauth2.Token, error) { return f.tok, nil }
func (f failingSaveStore) save(context.Context, *oauth2.Token) error {
	return errors.New("db down")
}

// PAC-122 SF-073: a renewed credential that cannot be stored is reported
// (with a rotating provider the stored refresh token is now dead); the
// action still gets the renewed token, as before.
func TestPAC122_RenewedTokenSaveFailureReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	d := oauthfake.New()
	defer d.Close()
	d.Rotate(true)
	owner := uuid.New()
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r-old", Expiry: past}
	s := &renewingSource{ctx: context.Background(), owner: owner, provider: storage.CredentialGoogle,
		cfg: googleCfg(d), store: failingSaveStore{expired}, cur: expired}
	tok, err := s.Token()
	if err != nil || tok.AccessToken != "access-1" {
		t.Fatalf("Token = %v, %v (behaviour changed)", tok, err)
	}
	got := rec.Matching("credential-renewal", "not stored: db down")
	if len(got) != 1 || got[0].Unit.ID != owner.String() {
		t.Fatalf("reports %+v", rec.All())
	}
	for _, secret := range []string{"r-old", "refresh-1", "access-1"} {
		if strings.Contains(got[0].Err.Error(), secret) {
			t.Fatalf("report carries a token: %v", got[0].Err)
		}
	}
}
