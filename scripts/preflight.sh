#!/usr/bin/env bash
# Run before every demo recording: everything Fritz needs, green or
# red, in one go. Hardware (camera, mic, Echo Dot) via
# check-hardware.sh, then the cloud side: the Meta API key, Gmail /
# Calendar / WhatsApp, the API server, and the Tailscale Funnel the
# WhatsApp webhook comes in through. Never prints secrets.
#
#   ~/Fritz/scripts/preflight.sh
#
# Google tokens in OAuth "Testing" mode die 7 days after googleauth;
# re-run it the day before recording even when this is all green.

set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${FRITZ_ENV_FILE:-/etc/fritz/fritz.env}"
failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }

if [ -r "$env_file" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$env_file"
    set +a
else
    fail "can't read $env_file"
fi

echo "== hardware"
"$repo/scripts/check-hardware.sh" || failures=$((failures + 1))

echo
echo "== face"
if systemctl is-active --quiet fritz-face; then
    pass "fritz-face (the eyes) is running"
else
    fail "fritz-face is not running — start Fritz with scripts/run-fritz.sh"
fi

echo
echo "== Meta API, mail, calendar, WhatsApp"
# mailcheck prints READY/FAIL per integration and exits non-zero on
# any failure; -spark adds one tiny Muse Spark call to prove the key.
(cd "$repo/api" && go run ./cmd/mailcheck -spark) || failures=$((failures + 1))

echo
echo "== API server"
port="${ADDR:-:8080}"
port="${port##*:}"
if curl -fsS --max-time 3 "http://127.0.0.1:$port/healthz" >/dev/null; then
    pass "fritz-server answers on :$port"
else
    fail "fritz-server is not answering on :$port — see ~/.cache/fritz/server.log"
fi

echo
echo "== WhatsApp webhook (Tailscale Funnel)"
if ! command -v tailscale >/dev/null 2>&1; then
    fail "tailscale is not installed — WhatsApp replies can't reach the Pi"
else
    funnel=$(tailscale funnel status 2>/dev/null || true)
    url=$(printf '%s\n' "$funnel" | grep -oE 'https://[^ ]+' | head -n 1)
    if [ -z "$url" ]; then
        fail "no Funnel is on — run: sudo tailscale funnel --bg --set-path /webhooks/whatsapp http://127.0.0.1:$port/webhooks/whatsapp"
    else
        # A GET without Meta's verify token must be refused by the
        # handler (403), which proves the request reached Fritz.
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "${url%/}/webhooks/whatsapp" || echo 000)
        case "$code" in
            400|401|403) pass "webhook reachable through $url (HTTP $code without a token, as expected)" ;;
            *) fail "webhook through $url answered HTTP $code — is fritz-server up and the Funnel pointing at :$port?" ;;
        esac
        # The rest of the API is personal (conversation, memories):
        # it must not answer from the internet.
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "${url%/}/turns" || echo 000)
        if [ "$code" = "200" ]; then
            fail "your conversation history is PUBLIC at ${url%/}/turns — run: sudo tailscale funnel reset, then the --set-path command above, and set FRITZ_APP_TOKEN"
        else
            pass "private routes are not public through the Funnel (HTTP $code)"
        fi
    fi
fi

echo
if [ "$failures" -gt 0 ]; then
    echo "$failures section(s) FAILED — fix these before recording"
    exit 1
fi
echo "All green. Reminder: re-run googleauth if it's been 6+ days."
