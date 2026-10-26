package config

import (
	"fmt"
	"strings"
)

// EnvInsecureCookies is the PAC-20 opt-out. Every cookie the backend sets
// carries the Secure attribute unless this is exactly "true" (case and
// surrounding spaces ignored). It exists for plain-HTTP origins only — local
// development on a non-localhost address, and the e2e stack. A production
// deployment never sets it.
const EnvInsecureCookies = "PACEDAY_INSECURE_COOKIES"

// DecideSecureCookies reads EnvInsecureCookies through getenv and reports
// whether cookies must carry Secure. It never fails and fails closed: unset,
// "false" and any unrecognised value keep Secure. notice is non-empty when the
// operator should be warned at startup.
func DecideSecureCookies(getenv func(string) string) (secure bool, notice string) {
	raw := strings.ToLower(strings.TrimSpace(getenv(EnvInsecureCookies)))
	switch raw {
	case "", "false":
		return true, ""
	case "true":
		return false, fmt.Sprintf("%s=true: cookies are set without the Secure attribute; this is for plain-HTTP development and e2e only, never production", EnvInsecureCookies)
	default:
		return true, fmt.Sprintf("%s=%q is not recognised (use \"true\" or leave unset); cookies keep the Secure attribute", EnvInsecureCookies, raw)
	}
}
