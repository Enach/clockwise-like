#!/usr/bin/env bash
# PAC-84 AC-4 [ops] (contract section 10): backup.sh refuses an env file that
# sets the encryption key, before looking up a container or writing anything.
#
# Needs bash only: a fake `docker` first on PATH records every call and
# reports no running postgres, so a run that passes the key check stops at
# the container lookup ("no running postgres service", exit 1).
#
# Usage: scripts/ops/backup_test.sh   (exit 0 when every case passes)
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
backup="$here/backup.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat > "$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "docker $*" >> "$DOCKER_CALLS"
exit 0
EOF
chmod +x "$work/bin/docker"
export PATH="$work/bin:$PATH" DOCKER_CALLS="$work/docker.calls"

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }

# run_case NAME CONTENT -> sets rc, err; fresh env file, out dir and call log.
run_case() {
  local name="$1" content="$2"
  env_file="$work/$name.env"
  out="$work/out-$name"
  printf '%b' "$content" > "$env_file"
  rm -f "$DOCKER_CALLS"
  err="$("$backup" --out "$out" --env-file "$env_file" --compose-file /nonexistent.yml 2>&1 >/dev/null)"
  rc=$?
}

expect_refused() {
  local name="$1" content="$2" var="$3" line="$4"
  run_case "$name" "$content"
  [ "$rc" -eq 2 ] || fail "$name: exit $rc, want 2 ($err)"
  local want="backup: $env_file line $line sets $var; the encryption key must live in encryption.env, outside the backup set (docs/runbook.md, \"Encryption key\"). Nothing was written."
  [ "$err" = "$want" ] || fail "$name: stderr\n got: $err\nwant: $want"
  [ ! -e "$out" ] || fail "$name: $out was created"
  [ ! -e "$DOCKER_CALLS" ] || fail "$name: docker was called: $(cat "$DOCKER_CALLS")"
  case "$err" in *s3cr3t*|*c2VjcmV0*) fail "$name: the value was printed" ;; esac
}

expect_passes_key_check() {
  local name="$1" content="$2"
  run_case "$name" "$content"
  [ "$rc" -eq 1 ] || fail "$name: exit $rc, want 1 at the container lookup ($err)"
  case "$err" in *"no running postgres service"*) ;; *) fail "$name: did not reach the container lookup: $err" ;; esac
  [ -e "$DOCKER_CALLS" ] || fail "$name: docker was never called"
  [ ! -e "$out" ] || fail "$name: $out was created before a container was found"
}

expect_refused active 'JWT_SECRET=x\nPACEDAY_ENCRYPTION_KEY=k1:c2VjcmV0s3cr3t=\n' PACEDAY_ENCRYPTION_KEY 2
expect_refused previous 'PACEDAY_ENCRYPTION_KEY_PREVIOUS=old:c2VjcmV0s3cr3t=\n' PACEDAY_ENCRYPTION_KEY_PREVIOUS 1
expect_refused export '# comment\n\n  export   PACEDAY_ENCRYPTION_KEY = k1:c2VjcmV0s3cr3t=\n' PACEDAY_ENCRYPTION_KEY 3
expect_refused double-quoted 'PACEDAY_ENCRYPTION_KEY="k1:c2VjcmV0s3cr3t="\n' PACEDAY_ENCRYPTION_KEY 1
expect_refused single-quoted "PACEDAY_ENCRYPTION_KEY='k1:c2VjcmV0s3cr3t='\n" PACEDAY_ENCRYPTION_KEY 1
expect_refused crlf 'A=1\r\nPACEDAY_ENCRYPTION_KEY=k1:c2VjcmV0s3cr3t=\r\n' PACEDAY_ENCRYPTION_KEY 2
expect_refused no-final-newline 'A=1\nPACEDAY_ENCRYPTION_KEY_PREVIOUS=old:c2VjcmV0s3cr3t=' PACEDAY_ENCRYPTION_KEY_PREVIOUS 2

expect_passes_key_check commented '# PACEDAY_ENCRYPTION_KEY=k1:c2VjcmV0s3cr3t=\n   #PACEDAY_ENCRYPTION_KEY_PREVIOUS=x\n'
expect_passes_key_check empty 'PACEDAY_ENCRYPTION_KEY=\nPACEDAY_ENCRYPTION_KEY_PREVIOUS=   \n'
expect_passes_key_check empty-quoted 'PACEDAY_ENCRYPTION_KEY=""\nexport PACEDAY_ENCRYPTION_KEY_PREVIOUS='"''"'\r\n'
expect_passes_key_check other-names 'MY_PACEDAY_ENCRYPTION_KEY=x\nPACEDAY_ENCRYPTION_KEYS=x\nE2E_ENCRYPTION_KEY=x\n'
expect_passes_key_check none 'JWT_SECRET=x\n'

if [ "$failures" -ne 0 ]; then
  echo "backup_test: $failures failure(s)" >&2
  exit 1
fi
echo "backup_test: ok (12 cases)"
