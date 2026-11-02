#!/usr/bin/env bash
# Restart Fritz on the Pi in one go: stop any running voice/vision
# loop and stray face instances, rebuild and reinstall the face if
# its code changed, restart fritz-face (the eyes), start the API server
# in the background (WhatsApp replies, phone app, dashboard), then run
# the voice loop (voicecheck: voice + memory + eyes following you) in
# the foreground. Ctrl-C stops the voice loop; the eyes and the server
# keep running.
#
# Run as the desktop login user (not with sudo), from anywhere:
#   ~/Fritz/scripts/run-fritz.sh            # rebuild if needed, restart, talk
#   ~/Fritz/scripts/run-fritz.sh --pull     # git pull first
#   ~/Fritz/scripts/run-fritz.sh --no-build # just restart, skip the builds
#   ~/Fritz/scripts/run-fritz.sh --no-server # don't (re)start the API server
#   ~/Fritz/scripts/run-fritz.sh --demo     # demo-recording timings (below)
# Extra arguments after `--` go to voicecheck.
# Server log: ~/.cache/fritz/server.log

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${FRITZ_ENV_FILE:-/etc/fritz/fritz.env}"
pull=0
build=1
server=1
demo=0
while [ $# -gt 0 ]; do
    case "$1" in
        --pull) pull=1 ;;
        --no-build) build=0 ;;
        --no-server) server=0 ;;
        --demo) demo=1 ;;
        --) shift; break ;;
        -h|--help) sed -n '2,18p' "$0"; exit 0 ;;
        *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
    esac
    shift
done

if [ "$(id -u)" -eq 0 ]; then
    echo "run this as your login user, not root — PipeWire (mic/speaker) lives in that session" >&2
    exit 1
fi
if [ ! -r "$env_file" ]; then
    echo "can't read $env_file — fix with: sudo chown $(id -un): $env_file && sudo chmod 600 $env_file" >&2
    exit 1
fi

# --- Stop what's running -------------------------------------------------
# One process owns the camera and mic, so an old voice/vision loop
# must go first. Face instances started by hand (outside systemd)
# would fight the service for the screen and UDP port 7070.
echo "== stopping old voice/vision loops and stray faces"
pkill -x voicecheck 2>/dev/null || true
pkill -x visioncheck 2>/dev/null || true
pkill -x voicetest 2>/dev/null || true
[ "$server" -eq 1 ] && { pkill -x fritz-server 2>/dev/null || true; }
# Bounded: a face stuck in a render call ignores SIGTERM, and an
# older unit without TimeoutStopSec would then wait 90 s here.
if ! sudo timeout 10 systemctl stop fritz-face 2>/dev/null; then
    echo "   fritz-face did not stop in 10 s — killing it"
    sudo systemctl kill -s KILL fritz-face 2>/dev/null || true
fi
pkill -x fritz_iot 2>/dev/null || true
sleep 1

if [ "$pull" -eq 1 ]; then
    echo "== git pull"
    git -C "$repo" pull --ff-only
fi

# --- Face (C++ eyes) ------------------------------------------------------
if [ "$build" -eq 1 ]; then
    echo "== building the face"
    cmake -S "$repo/iot" -B "$repo/iot/build" -DCMAKE_BUILD_TYPE=Release >/dev/null
    cmake --build "$repo/iot/build" -j"$(nproc)"
    # Only touch /opt when the binary actually changed.
    if ! cmp -s "$repo/iot/build/fritz_iot" /opt/fritz/fritz_iot; then
        echo "== installing the new face binary"
        sudo mkdir -p /opt/fritz
        sudo install -m755 "$repo/iot/build/fritz_iot" /opt/fritz/fritz_iot
    fi
    if ! cmp -s "$repo/iot/systemd/fritz-face.service" /etc/systemd/system/fritz-face.service; then
        echo "== installing the updated fritz-face.service"
        sudo cp "$repo/iot/systemd/fritz-face.service" /etc/systemd/system/
        sudo systemctl daemon-reload
        sudo systemctl enable fritz-face >/dev/null
    fi
fi

echo "== starting the eyes"
sudo systemctl restart fritz-face
for _ in 1 2 3 4 5; do
    systemctl is-active --quiet fritz-face && break
    sleep 1
done
if ! systemctl is-active --quiet fritz-face; then
    echo "fritz-face did not start — last log lines:" >&2
    journalctl -u fritz-face -n 20 --no-pager >&2 || true
    exit 1
fi

# --- Voice loop (Go) -------------------------------------------------------
# Build to a real binary (instead of go run) so the next run of this
# script can find and stop it by name.
cd "$repo/api"
set -a
# shellcheck disable=SC1090
. "$env_file"
set +a
# The voice loop and the server share one database: the sentry switch,
# what was already alerted, and your WhatsApp replies meet there.
export FRITZ_DB="${FRITZ_DB:-$repo/api/fritz.db}"

# --- Demo timings (--demo) --------------------------------------------------
# Tuned for a recording, not daily use: things happen within seconds
# of the cue, and background camera load stays low so SAM stays
# under its rate limit. Timings already set in the env file win;
# the watch list comes from FRITZ_DEMO_OBJECTS (default: keys).
if [ "$demo" -eq 1 ]; then
    echo "== demo mode: fast greeting and mail, light background vision"
    export FRITZ_WATCH_OBJECTS="${FRITZ_DEMO_OBJECTS:-keys}" # only what the demo asks for
    export FRITZ_GREET_AWAY_MS="${FRITZ_GREET_AWAY_MS:-15000}"   # greet after 15 s away
    export FRITZ_GREET_EVERY_MS="${FRITZ_GREET_EVERY_MS:-30000}"
    export FRITZ_MAIL_POLL_MS="${FRITZ_MAIL_POLL_MS:-20000}"     # a sent email shows up in ~20 s
    export FRITZ_SENTRY_COOLDOWN_MS="${FRITZ_SENTRY_COOLDOWN_MS:-30000}"
    export FRITZ_TRACK_HZ="${FRITZ_TRACK_HZ:-1}"
fi
if [ "$build" -eq 1 ] || [ ! -x bin/voicecheck ]; then
    echo "== building the voice loop"
    go build -o bin/voicecheck ./cmd/voicecheck
fi

# --- API server (background) -------------------------------------------------
if [ "$server" -eq 1 ]; then
    if [ "$build" -eq 1 ] || [ ! -x bin/fritz-server ]; then
        echo "== building the API server"
        go build -o bin/fritz-server ./cmd/server
    fi
    log_dir="$HOME/.cache/fritz"
    mkdir -p "$log_dir"
    echo "== starting the API server (WhatsApp replies; log: $log_dir/server.log)"
    nohup ./bin/fritz-server >>"$log_dir/server.log" 2>&1 &
    sleep 1
    if ! kill -0 $! 2>/dev/null; then
        echo "   the API server exited — last log lines:" >&2
        tail -n 15 "$log_dir/server.log" >&2 || true
    fi
fi

echo "== Fritz is up — say \"Hi Fritz\" (Ctrl-C stops the voice loop; the eyes keep running)"
exec ./bin/voicecheck "$@"
