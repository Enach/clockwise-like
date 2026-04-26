package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
)

// DetectResult is returned by the /api/auth/detect endpoint.
type DetectResult struct {
	Type         string `json:"type"`                    // "google" | "microsoft" | "sso" | "generic"
	ProviderName string `json:"provider_name,omitempty"` // only when type == "sso"
	RedirectURL  string `json:"redirect_url,omitempty"`  // only when type == "sso"
}

// DetectAuthProvider inspects an email address and returns the correct auth provider.
func DetectAuthProvider(ctx context.Context, db *sql.DB, email string) (*DetectResult, error) {
	d := domain.ExtractDomain(email)
	if d == "" {
		return &DetectResult{Type: "generic"}, nil
	}

	// Generic consumer domains get no SSO check.
	if domain.IsGenericDomain(d) {
		return &DetectResult{Type: "generic"}, nil
	}

	// Custom SSO configured for this domain takes priority.
	provider, err := storage.GetSSOProviderByDomain(db, d)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		return &DetectResult{
			Type:         "sso",
			ProviderName: provider.ProviderName,
			RedirectURL:  fmt.Sprintf("/api/auth/sso/%s", d),
		}, nil
	}

	// Microsoft tenant heuristic (2-second timeout, 1-hour cache).
	isMicrosoft, _ := isMicrosoftTenant(ctx, d)
	if isMicrosoft {
		return &DetectResult{Type: "microsoft"}, nil
	}

	return &DetectResult{Type: "google"}, nil
}

// --- Microsoft tenant detection ------------------------------------------

type tenantEntry struct {
	result    bool
	expiresAt time.Time
}

var tenantCache struct {
	sync.RWMutex
	m map[string]tenantEntry
}

func init() { tenantCache.m = make(map[string]tenantEntry) }

func isMicrosoftTenant(ctx context.Context, d string) (bool, error) {
	tenantCache.RLock()
	e, ok := tenantCache.m[d]
	tenantCache.RUnlock()
	if ok && time.Now().Before(e.expiresAt) {
		return e.result, nil
	}

	url := fmt.Sprintf(
		"https://login.microsoftonline.com/%s/.well-known/openid-configuration", d)
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cacheTenant(d, false)
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cacheTenant(d, false)
		return false, nil
	}

	var body struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}

	// "common" / "organizations" issuers mean it is not a specific tenant.
	isTenant := body.Issuer != "" &&
		!strings.Contains(body.Issuer, "/common/") &&
		!strings.Contains(body.Issuer, "/organizations/")
	cacheTenant(d, isTenant)
	return isTenant, nil
}

func cacheTenant(d string, result bool) {
	tenantCache.Lock()
	tenantCache.m[d] = tenantEntry{result: result, expiresAt: time.Now().Add(time.Hour)}
	tenantCache.Unlock()
}
