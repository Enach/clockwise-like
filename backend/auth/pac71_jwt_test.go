package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// PAC-71 CONTRACT-4: the session generation claim `gen`.
func TestPAC71_CONTRACT4_GenClaim(t *testing.T) {
	const secret = "s"
	id := uuid.New()
	sign := func(gen any, set bool) string {
		c := jwt.MapClaims{"sub": id.String(), "email": "x@y.test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
		if set {
			c["gen"] = gen
		}
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	for name, c := range map[string]struct {
		gen   any
		set   bool
		valid bool
		want  int64
	}{
		"absent reads as 0": {nil, false, true, 0},
		"0":                 {0, true, true, 0},
		"7":                 {7, true, true, 7},
		"string \"0\"":      {"0", true, false, 0},
		"null":              {nil, true, false, 0},
		"-1":                {-1, true, false, 0},
		"1.5":               {1.5, true, false, 0},
		"overflow":          {json.Number("99999999999999999999"), true, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			claims, err := ValidateToken(sign(c.gen, c.set), secret)
			if (err == nil) != c.valid {
				t.Fatalf("valid = %v, want %v", err == nil, c.valid)
			}
			if c.valid {
				if g, _ := claims.Generation(); g != c.want {
					t.Fatalf("gen = %d", g)
				}
			}
		})
	}
	tok, err := GenerateSessionToken(id, "x@y.test", 3, secret)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateToken(tok, secret)
	if g, _ := claims.Generation(); err != nil || g != 3 {
		t.Fatal("issued gen not read back")
	}
	if _, err := GenerateSessionToken(id, "x", -1, secret); err == nil {
		t.Fatal("negative gen issued")
	}
	// Every issued token carries gen (generation 0 included).
	plain, _ := GenerateToken(id, "x@y.test", secret)
	payload, _ := base64.RawURLEncoding.DecodeString(splitPayload(plain))
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if v, ok := m["gen"]; !ok || v != float64(0) {
		t.Fatalf("payload %s", payload)
	}
}

func splitPayload(tok string) string {
	start := 0
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			if start == 0 {
				start = i + 1
			} else {
				return tok[start:i]
			}
		}
	}
	return ""
}
