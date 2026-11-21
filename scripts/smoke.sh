#!/usr/bin/env bash
# Smoke test of a running stand: admin login, customer registration, role
# checks, meter type catalogue (L1); gateways and batches of readings (L2).
# Exits non-zero on the first failure.
#
#   set -a; . ./.env; set +a        # ADMIN_LOGIN, ADMIN_PASSWORD, KEY
#   scripts/smoke.sh                # BASE_URL defaults to http://localhost:8080
#
# KEY must be the server's HashSHA256 key; empty means the server runs
# without signing, and the signature checks are skipped.
#
# Needs curl, jq, openssl and python3 (HMAC with the key taken from the
# environment). Passwords, tokens and keys never appear on a command line
# (they would be visible in ps): request bodies and the Authorization and
# X-Gateway-Key headers go through files in a private temp directory; jq
# and python3 read secrets from the environment. Each run registers a new
# customer (random password), adds one subtype (smoke_<time>) to "gas",
# creates a gateway smoke-<time> and a pending device SMOKE-<time>.
set -euo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
: "${ADMIN_LOGIN:?set ADMIN_LOGIN (e.g. set -a; . ./.env; set +a)}"
: "${ADMIN_PASSWORD:?set ADMIN_PASSWORD}"
export ADMIN_LOGIN ADMIN_PASSWORD
KEY=${KEY:-}
export KEY

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

# --- L2: gateways and readings ---------------------------------------------

# sign FILE — hex HMAC-SHA256 of the file with $KEY (read from the environment).
sign() {
	python3 -I -c 'import hashlib, hmac, os, sys
print(hmac.new(os.environb[b"KEY"], sys.stdin.buffer.read(), hashlib.sha256).hexdigest())' <"$1"
}

# gw_send MODE — POSTs $tmp/batch to /gateway/v1/readings as the smoke
# gateway. MODE: ok, nokey (no X-Gateway-Key), badsig (wrong signature).
# Leaves the status in $code, the body in $tmp/body, headers in $tmp/headers.
gw_send() {
	local mode=$1
	local args=(-sS -o "$tmp/body" -D "$tmp/headers" -w '%{http_code}' -X POST
		-H 'Content-Type: application/json' --data-binary "@$tmp/batch")
	if [ "$mode" != nokey ]; then args+=(-H "@$tmp/gwkey"); fi
	if [ -n "$KEY" ]; then
		if [ "$mode" = badsig ]; then
			args+=(-H "HashSHA256: $(printf '%064d' 0)")
		else
			args+=(-H "HashSHA256: $(sign "$tmp/batch")")
		fi
	fi
	code=$(curl "${args[@]}" "$BASE_URL/gateway/v1/readings")
}

# signed — the answer carries a valid HashSHA256 (skipped without KEY).
signed() {
	[ -n "$KEY" ] || return 0
	local got
	got=$(awk 'tolower($1) == "hashsha256:" { print $2 }' "$tmp/headers" | tr -d '\r')
	if [ -n "$got" ] && [ "$got" = "$(sign "$tmp/body")" ]; then ok "answer is signed"; else fail "answer signature: got '$got'"; fi
}

# batch ID — one new device with its state and one reading an hour ago.
batch() {
	jq -n --arg id "$1" --arg serial "SMOKE-$run_id" '{
		batch_id: $id,
		items: [{device_serial: $serial, model: "Smoke-1",
			device_status: {battery_pct: 90, rssi: -100, error_codes: []},
			readings: [{measured_at: (now - 3600 | floor | todate), value: "1.000"}]}]
	}' >"$tmp/batch"
}

jq -n --arg name "smoke-$run_id" '{name: $name}' >"$tmp/req"
call POST /api/admin/gateways "$customer"
expect 403 "customer creates a gateway"

jq -n --arg name "smoke-$run_id" '{name: $name}' >"$tmp/req"
call POST /api/admin/gateways "$admin"
expect 201 "admin creates gateway smoke-$run_id"
gw_id=$(jq -er '.id' "$tmp/body") || fail "no id in the response"
jq -er '"X-Gateway-Key: " + .key' "$tmp/body" >"$tmp/gwkey" || fail "no key in the response"

call GET /api/admin/gateways "$admin"
expect 200 "GET /api/admin/gateways"
if jq -e --argjson id "$gw_id" 'any(.[]; .id == $id) and all(.[]; has("key") | not)' "$tmp/body" >/dev/null; then
	ok "gateway is listed, no keys in the list"
else
	fail "gateway $gw_id is not listed or a key is shown"
fi

batch "smoke-$run_id-1"
gw_send ok
expect 200 "batch with a new device"
signed
if jq -e --arg serial "SMOKE-$run_id" '.accepted == 0 and .rejected == [{device_serial: $serial, index: 0, reason: "device_not_active"}]' "$tmp/body" >/dev/null; then
	ok "new device is pending: device_not_active"
else
	fail "unexpected answer for a new device"
fi
cp "$tmp/body" "$tmp/first"

gw_send ok
expect 200 "the same batch_id again"
if cmp -s "$tmp/first" "$tmp/body"; then ok "repeat returns the same answer"; else fail "repeat answered differently"; fi

gw_send nokey
expect 401 "batch without X-Gateway-Key"
signed

if [ -n "$KEY" ]; then
	gw_send badsig
	expect 400 "batch with a wrong HashSHA256"
	signed
else
	echo "  skip signature checks: KEY is not set"
fi

call POST "/api/admin/gateways/$gw_id/block" "$admin"
expect 200 "block the gateway"
batch "smoke-$run_id-2"
gw_send ok
expect 403 "batch from a blocked gateway"
signed

call POST "/api/admin/gateways/$gw_id/unblock" "$admin"
expect 200 "unblock the gateway"
gw_send ok
expect 200 "batch after unblocking"

echo "all $pass checks passed"
