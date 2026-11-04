#!/usr/bin/env bash
# Install Fritz's WirePlumber config (scripts/wireplumber/*.conf) into the
# login user's ~/.config and restart WirePlumber so it takes effect.
#   50-fritz-bt-roles.conf       Pi only sends audio over Bluetooth, so the
#                                Echo can't grab the Pi as its speaker
#   51-fritz-echo-nosuspend.conf never suspend the Echo sink (idle drops)
#
# Run as the desktop login user (not with sudo): PipeWire lives in that
# session. Safe to re-run.

set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
    echo "run this as your login user, not root — PipeWire lives in that session" >&2
    exit 1
fi

src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/wireplumber"
dest="${XDG_CONFIG_HOME:-$HOME/.config}/wireplumber/wireplumber.conf.d"

mkdir -p "$dest"
for f in "$src"/*.conf; do
    install -m 644 "$f" "$dest/"
    echo "installed $dest/$(basename "$f")"
done

systemctl --user restart wireplumber
echo "restarted wireplumber"

# The role change only applies to new connections. If the Echo was linked
# the wrong way round, connect from the Pi's side so it comes up as a sink.
if [ -n "${FRITZ_SPEAKER_BT:-}" ]; then
    sleep 2
    bluetoothctl trust "$FRITZ_SPEAKER_BT" >/dev/null
    bluetoothctl connect "$FRITZ_SPEAKER_BT" || true
else
    echo "FRITZ_SPEAKER_BT not set: reconnect the Echo with 'bluetoothctl connect <MAC>'"
fi
