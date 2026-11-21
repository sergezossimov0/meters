#!/usr/bin/env bash
# Registers the two gateways of the docker compose stand and prints their API
# keys as lines for the stand's environment file:
#
#   set -a; . ./.env; set +a        # ADMIN_LOGIN, ADMIN_PASSWORD
#   scripts/gateway-keys.sh         # BASE_URL defaults to http://localhost:8080
#   GATEWAY_1_KEY=gwk_...
#   GATEWAY_2_KEY=gwk_...
#
# Copy the two lines into .env yourself, then start the gateways:
# COMPOSE_PROFILES=gateways mise run up. The script does not edit .env: a
# file with secrets is changed by its owner only. A key is shown only once
# (the server keeps its sha256), so running the script again creates two
# more gateways with new keys; block the old ones in the admin API.
#
# Needs curl and jq. The admin password goes through a file in a private
# temp directory, never on a command line.
set -euo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
: "${ADMIN_LOGIN:?set ADMIN_LOGIN (e.g. set -a; . ./.env; set +a)}"
: "${ADMIN_PASSWORD:?set ADMIN_PASSWORD}"
export ADMIN_LOGIN ADMIN_PASSWORD

umask 077
tmp=$(mktemp -d "${TMPDIR:-/tmp}/gwkeys.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

die() {
	printf 'error: %s\n' "$1" >&2
	if [ -s "$tmp/body" ]; then printf '       %s\n' "$(head -c 300 "$tmp/body")" >&2; fi
	exit 1
}

# post PATH — sends $tmp/req; status in $code, answer in $tmp/body.
post() {
	local args=(-sS -o "$tmp/body" -w '%{http_code}' -X POST
		-H 'Content-Type: application/json' --data-binary "@$tmp/req")
	if [ -s "$tmp/auth" ]; then args+=(-H "@$tmp/auth"); fi
	code=$(curl "${args[@]}" "$BASE_URL$1") || die "cannot reach $BASE_URL"
}

jq -n '{login: env.ADMIN_LOGIN, password: env.ADMIN_PASSWORD}' >"$tmp/req"
post /api/user/login
[ "$code" = 200 ] || die "admin login: $code"
jq -er '"Authorization: Bearer " + .token' "$tmp/body" >"$tmp/auth" || die "no token in the answer"

suffix=$(date +%Y%m%d-%H%M%S)
for n in 1 2; do
	jq -n --arg name "gateway-$n ($suffix)" '{name: $name}' >"$tmp/req"
	post /api/admin/gateways
	[ "$code" = 201 ] || die "create gateway-$n: $code"
	jq -er --arg n "$n" '"GATEWAY_\($n)_KEY=" + .key' "$tmp/body" || die "no key in the answer"
	printf 'created gateway-%s: id %s\n' "$n" "$(jq -r .id "$tmp/body")" >&2
done
