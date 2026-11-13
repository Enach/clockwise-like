package auth

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is a Paceday session. Gen is PAC-71's session generation claim
// `gen` (spec docs/specs/PAC-71.md §3 "Sessions"; contract bearerAuth): kept
// raw so that an absent claim (every token issued before PAC-71) reads as 0
// while a present one that is not a non-negative integer literal ("0", null,
// -1, 1.5) makes the token invalid.
type Claims struct {
	UserID string          `json:"sub"`
	Email  string          `json:"email"`
	Gen    json.RawMessage `json:"gen,omitempty"`
	jwt.RegisteredClaims
}

var genLiteral = regexp.MustCompile(`^[0-9]+$`)

// Generation returns the token's session generation: 0 when the claim is
// absent, an error when it is present but not a non-negative integer.
func (c *Claims) Generation() (int64, error) {
	if len(c.Gen) == 0 {
		return 0, nil
	}
	s := string(c.Gen)
	if !genLiteral.MatchString(s) {
		return 0, fmt.Errorf("invalid gen claim")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid gen claim")
	}
	return n, nil
}

// GenerateToken mints a session at generation 0 (a new account's).
func GenerateToken(userID uuid.UUID, email, secret string) (string, error) {
	return GenerateSessionToken(userID, email, 0, secret)
}

// GenerateSessionToken mints a 7-day session carrying gen, the account's
// current session generation at issue time.
func GenerateSessionToken(userID uuid.UUID, email string, gen int64, secret string) (string, error) {
	if gen < 0 {
		return "", fmt.Errorf("negative session generation")
	}
	claims := Claims{
		UserID: userID.String(),
		Email:  email,
		Gen:    json.RawMessage(strconv.FormatInt(gen, 10)),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ValidateToken checks the signature (HS256 only), expiry and the gen claim's
// form. It does not compare gen with the account's current generation; that
// needs the database (requireAuth).
func ValidateToken(tokenStr, secret string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if _, err := claims.Generation(); err != nil {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
