package api

import "net/http"

// secureCookies is whether setCookie adds the Secure attribute (PAC-20,
// API-007). It defaults to true so that a caller that never configures it
// still gets the production behaviour; main.go sets it from
// config.DecideSecureCookies at startup, before any route exists.
var secureCookies = true

// SetSecureCookies configures whether every cookie the API sets carries
// Secure. Call once at startup; false is for plain-HTTP dev and e2e only.
func SetSecureCookies(secure bool) { secureCookies = secure }

// setCookie is the only way this package sets a cookie
// (TestPAC20_NoCookieBypassesSetCookieHelper enforces it). It applies the
// deployment's Secure decision, so no call site can forget it — including the
// MaxAge=-1 deletions, which a browser ignores for a Secure cookie unless the
// deletion is itself Secure.
func setCookie(w http.ResponseWriter, c *http.Cookie) {
	c.Secure = secureCookies
	http.SetCookie(w, c)
}
