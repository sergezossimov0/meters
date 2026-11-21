#!/usr/bin/env bash
# Smoke test of a running stand (L1): admin login, customer registration,
# role checks, meter type catalogue. Exits non-zero on the first failure.
#
#   set -a; . ./.env; set +a        # ADMIN_LOGIN, ADMIN_PASSWORD
#   scripts/smoke.sh                # BASE_URL defaults to http://localhost:8080
#
# Needs curl, jq and openssl. Passwords and tokens never appear on a command
# line (they would be visible in ps): request bodies and the Authorization
# header go through files in a private temp directory, and jq reads the
# passwords from the environment. Each run registers a new customer (random
# password) and adds one subtype (smoke_<time>) to "gas" in the stand's DB.
set -euo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
: "${ADMIN_LOGIN:?set ADMIN_LOGIN (e.g. set -a; . ./.env; set +a)}"
: "${ADMIN_PASSWORD:?set ADMIN_PASSWORD}"
export ADMIN_LOGIN ADMIN_PASSWORD

umask 077
tmp=$(mktemp -d "${TMPDIR:-/tmp}/smoke.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

run_id=$(date +%s)
CUSTOMER_LOGIN="smoke-$run_id"
CUSTOMER_PASSWORD=$(openssl rand -hex 12)
SUBTYPE_CODE="smoke_$run_id"
export CUSTOMER_LOGIN CUSTOMER_PASSWORD

pass=0
ok() { pass=$((pass + 1)); printf '  ok   %s\n' "$1"; }
fail() {
	printf '  FAIL %s\n' "$1" >&2
	if [ -s "$tmp/body" ]; then printf '       body: %s\n' "$(head -c 300 "$tmp/body")" >&2; fi
	exit 1
}

# call METHOD PATH [TOKEN] — sends $tmp/req (if not empty) as JSON, leaves the
# status in $code and the response in $tmp/body.
call() {
	local method=$1 path=$2 token=${3:-}
	local args=(-sS -o "$tmp/body" -w '%{http_code}' -X "$method")
	if [ -s "$tmp/req" ]; then
		args+=(-H 'Content-Type: application/json' --data-binary "@$tmp/req")
	fi
	if [ -n "$token" ]; then
		printf 'Authorization: Bearer %s\n' "$token" >"$tmp/auth"
		args+=(-H "@$tmp/auth")
	fi
	code=$(curl "${args[@]}" "$BASE_URL$path")
	: >"$tmp/req"
	rm -f "$tmp/auth"
}

expect() { # expect CODE DESCRIPTION
	if [ "$code" = "$1" ]; then ok "$2 → $1"; else fail "$2: got $code, want $1"; fi
}

token() { jq -er '.token' "$tmp/body" || fail "no token in the response"; }

subtype_req() {
	jq -n --arg code "$SUBTYPE_CODE" '{type_code: "gas", code: $code, name: "Smoke test", unit: "m3"}' >"$tmp/req"
}

echo "smoke test against $BASE_URL"
: >"$tmp/req"

call GET /ping
expect 200 "GET /ping"

jq -n '{login: env.ADMIN_LOGIN, password: env.ADMIN_PASSWORD}' >"$tmp/req"
call POST /api/user/login
expect 200 "admin login"
admin=$(token)

# The customer's random password is certainly not the admin's.
jq -n '{login: env.ADMIN_LOGIN, password: env.CUSTOMER_PASSWORD}' >"$tmp/req"
call POST /api/user/login
expect 401 "admin login with a wrong password"

jq -n '{login: env.CUSTOMER_LOGIN, password: env.CUSTOMER_PASSWORD}' >"$tmp/req"
call POST /api/user/register
expect 200 "register customer $CUSTOMER_LOGIN"
customer=$(token)

jq -n '{login: env.CUSTOMER_LOGIN, password: env.CUSTOMER_PASSWORD}' >"$tmp/req"
call POST /api/user/register
expect 409 "register the same login again"

call GET /api/meter-types
expect 401 "GET /api/meter-types without a token"

call GET /api/meter-types "$customer"
expect 200 "GET /api/meter-types as customer"
n=$(jq '[.[].subtypes[]] | length' "$tmp/body")
if [ "$n" -ge 8 ]; then ok "catalogue has $n subtypes (8 from the migration)"; else fail "catalogue has $n subtypes, want at least 8"; fi

subtype_req
call POST /api/admin/meter-types "$customer"
expect 403 "customer adds a subtype"

subtype_req
call POST /api/admin/meter-types "$admin"
expect 201 "admin adds subtype gas/$SUBTYPE_CODE"

subtype_req
call POST /api/admin/meter-types "$admin"
expect 409 "admin adds the same subtype again"

call GET /api/meter-types "$customer"
expect 200 "GET /api/meter-types after the change"
if jq -e --arg code "$SUBTYPE_CODE" '.[] | select(.code == "gas") | .subtypes[] | select(.code == $code)' "$tmp/body" >/dev/null; then
	ok "new subtype is visible to the customer"
else
	fail "new subtype is not in the catalogue"
fi

echo "all $pass checks passed"
