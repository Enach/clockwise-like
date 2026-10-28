#!/bin/sh
# PAC-81 Part B — nginx runtime settings (docs/plans/PAC-81-part-b.md §1.1).
#
# Run as the nginx container's entrypoint with /bin/sh (no executable bit is
# needed on a bind mount):
#
#   entrypoint: ["/bin/sh", "/paceday/render.sh"]
#   command:    ["nginx", "-g", "daemon off;"]
#
# It validates the settings below, writes the http-level runtime file that
# nginx/nginx.conf depends on, then hands over to the image's own entrypoint.
# An invalid value stops the container: nginx never runs with a policy the
# operator did not mean. nginx/nginx.conf refers to names only this file
# defines, so the config cannot load without this step either.
#
# Settings (all optional):
#   NGINX_TRUSTED_PROXIES      IPs/CIDRs (comma or space separated) whose
#                              X-Forwarded-For and X-Forwarded-Proto are
#                              believed. Empty (default): nothing is trusted
#                              and the socket peer is the client.
#   NGINX_CSP_SENTRY_ORIGIN    The frontend's Sentry ingest origin, allowed in
#                              connect-src. Empty = the default below, which is
#                              the origin of the web's built-in DSN; it must
#                              match the DSN the frontend image was built with.
#   NGINX_CSP_CONNECT_ORIGINS  Extra connect-src origins.
#   API_BASE_URL               The frontend's setting. When absolute, its
#                              origin is allowed in connect-src.
#   NGINX_BACKEND_UPSTREAM     host:port of the backend  (default backend:8080)
#   NGINX_FRONTEND_UPSTREAM    host:port of the frontend (default frontend:80)
#
# TLS and HSTS are deliberately absent: the proxy in front terminates TLS
# (owner decision 2026-10-27).
set -eu

OUT=/etc/nginx/conf.d/00-paceday-runtime.conf
DEFAULT_SENTRY_ORIGIN=https://o4512081160896512.ingest.de.sentry.io

fail() {
	echo "paceday-render: $*" >&2
	exit 1
}

# True when $1 holds no control character (newline, carriage return, tab,
# ...). The checks below use grep, which matches line by line: without this,
# a valid first line would carry everything after a newline into the
# generated config. Compared by byte count, because $(...) would strip a
# trailing newline from the value itself.
is_plain() {
	[ "$(printf '%s' "$1" | wc -c)" -eq "$(printf '%s' "$1" | tr -d '[:cntrl:]' | wc -c)" ]
}

# Origins are scheme://host[:port] only: no path, no wildcard, nothing that
# could end a CSP directive or the nginx string it is written into.
is_origin() {
	is_plain "$1" || return 1
	printf '%s' "$1" | grep -Eq '^https?://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$'
}

# An IP or CIDR. A /0 prefix would trust every address, so it is refused.
is_address() {
	is_plain "$1" || return 1
	printf '%s' "$1" | grep -Eq '^[0-9A-Fa-f:.]*[0-9][0-9A-Fa-f:.]*(/[0-9]{1,3})?$' || return 1
	case "$1" in
	*/0 | */00 | */000) return 1 ;;
	esac
}

is_upstream() {
	is_plain "$1" || return 1
	printf '%s' "$1" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?:[0-9]{1,5}$'
}

# Split a list on commas and whitespace.
items() {
	printf '%s' "$1" | tr ',' ' '
}

# Refuse a control character in any raw setting before anything is split,
# matched or rewritten (a newline is not accepted as a list separator either).
check_plain() {
	is_plain "$2" || fail "$1 must not contain a control character (newline, carriage return, tab, ...)"
}
check_plain NGINX_TRUSTED_PROXIES "${NGINX_TRUSTED_PROXIES:-}"
check_plain NGINX_CSP_SENTRY_ORIGIN "${NGINX_CSP_SENTRY_ORIGIN:-}"
check_plain NGINX_CSP_CONNECT_ORIGINS "${NGINX_CSP_CONNECT_ORIGINS:-}"
check_plain API_BASE_URL "${API_BASE_URL:-}"
check_plain NGINX_BACKEND_UPSTREAM "${NGINX_BACKEND_UPSTREAM:-}"
check_plain NGINX_FRONTEND_UPSTREAM "${NGINX_FRONTEND_UPSTREAM:-}"

backend=${NGINX_BACKEND_UPSTREAM:-backend:8080}
frontend=${NGINX_FRONTEND_UPSTREAM:-frontend:80}
is_upstream "$backend" || fail "NGINX_BACKEND_UPSTREAM must be host:port, got '$backend'"
is_upstream "$frontend" || fail "NGINX_FRONTEND_UPSTREAM must be host:port, got '$frontend'"

# Disable globbing while splitting lists: an entry must never expand to files.
set -f

proxies=""
for p in $(items "${NGINX_TRUSTED_PROXIES:-}"); do
	is_address "$p" || fail "NGINX_TRUSTED_PROXIES entries must be IP addresses or CIDRs (not /0), got '$p'"
	proxies="$proxies $p"
done

connect="'self'"
add_connect() {
	case " $connect " in
	*" $1 "*) ;;
	*) connect="$connect $1" ;;
	esac
}

sentry=${NGINX_CSP_SENTRY_ORIGIN:-$DEFAULT_SENTRY_ORIGIN}
is_origin "$sentry" || fail "NGINX_CSP_SENTRY_ORIGIN must be an origin (scheme://host[:port]), got '$sentry'"
add_connect "$sentry"

api=${API_BASE_URL:-}
case "$api" in
"" | /*) ;; # unset or same-origin relative: nothing to add
http://* | https://*)
	api_origin=$(printf '%s' "$api" | sed -E 's#^(https?://[^/?#]*).*$#\1#')
	is_origin "$api_origin" || fail "API_BASE_URL has no usable origin: '$api'"
	add_connect "$api_origin"
	;;
*) fail "API_BASE_URL must be an absolute http(s) URL or a path, got '$api'" ;;
esac

for o in $(items "${NGINX_CSP_CONNECT_ORIGINS:-}"); do
	is_origin "$o" || fail "NGINX_CSP_CONNECT_ORIGINS entries must be origins (scheme://host[:port]), got '$o'"
	add_connect "$o"
done

set +f

# D-8. Enforced, not report-only: AC-B3 proves zero violations in e2e.
# One deviation from D-8: font-src also allows data:, because FullCalendar
# embeds its icon font (fcicons) as a data: URI (docs/plans/PAC-81-part-b.md).
csp="default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data: https://*.googleusercontent.com; connect-src $connect; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

{
	echo "# Generated by /paceday/render.sh at container start. Do not edit."
	echo "upstream paceday_backend {"
	echo "    server $backend;"
	echo "    keepalive 16;"
	echo "}"
	echo "upstream paceday_frontend {"
	echo "    server $frontend;"
	echo "    keepalive 16;"
	echo "}"
	echo
	echo "# 1 when the socket peer is a configured trusted proxy."
	echo "geo \$realip_remote_addr \$paceday_peer_trusted {"
	echo "    default 0;"
	for p in $proxies; do
		echo "    $p 1;"
	done
	echo "}"
	if [ -n "$proxies" ]; then
		echo
		echo "# D-9: the client is the right-most X-Forwarded-For entry that is not a"
		echo "# trusted proxy."
		for p in $proxies; do
			echo "set_real_ip_from $p;"
		done
		echo "real_ip_header X-Forwarded-For;"
		echo "real_ip_recursive on;"
	fi
	echo
	echo "map \$host \$paceday_csp {"
	echo "    default \"$csp\";"
	echo "}"
} >"$OUT"

echo "paceday-render: wrote $OUT (trusted proxies:${proxies:- none}; connect-src $connect)"

exec /docker-entrypoint.sh "$@"
