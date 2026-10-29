#!/usr/bin/env bash
# Fritz hardware checks for the Raspberry Pi: Logitech C922 webcam
# (camera + mic) and Echo Dot Bluetooth speaker. Prints PASS/FAIL per
# check and exits non-zero if anything fails. Run this first whenever
# something "doesn't work" on the Pi. See .agents/skills/fritz-c922.
#
# Env overrides: FRITZ_CAMERA_DEVICE, FRITZ_MIC (default pipewire),
# FRITZ_SPEAKER_BT (Echo MAC; skips nothing, FAILs with a hint if unset).

failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }

# Load Fritz config (FRITZ_SPEAKER_BT etc.) if it isn't already in the
# environment, so running this by hand works the same as under systemd.
env_file="${FRITZ_ENV_FILE:-/etc/fritz/fritz.env}"
if [ -z "${FRITZ_SPEAKER_BT:-}" ] && [ -r "$env_file" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$env_file"
    set +a
fi

have() { command -v "$1" >/dev/null 2>&1; }

# --- Camera device ------------------------------------------------------
cam="${FRITZ_CAMERA_DEVICE:-}"
if [ -z "$cam" ]; then
    # Same fallback the Go code uses: C922 by-id path, else first index0.
    cam=$(ls /dev/v4l/by-id/*C922*-video-index0 2>/dev/null | head -n 1)
    [ -z "$cam" ] && cam=$(ls /dev/v4l/by-id/*-video-index0 2>/dev/null | head -n 1)
fi
if [ -n "$cam" ] && [ -e "$cam" ]; then
    pass "C922 camera present: $cam"
else
    fail "C922 camera not found in /dev/v4l/by-id (ls /dev/v4l/by-id/ | grep -i c922)"
    cam=""
fi

# --- One MJPEG frame -----------------------------------------------------
if [ -n "$cam" ] && have ffmpeg; then
    frame=$(mktemp /tmp/fritz-c922-XXXXXX.jpg)
    if ffmpeg -hide_banner -loglevel error -f v4l2 -input_format mjpeg \
            -video_size 1280x720 -i "$cam" -frames:v 1 -y "$frame" \
            >/dev/null 2>&1 && [ -s "$frame" ]; then
        pass "captured one MJPEG frame ($(stat -c%s "$frame" 2>/dev/null || stat -f%z "$frame") bytes)"
    else
        fail "could not capture a frame from $cam (is another process holding the camera?)"
    fi
    rm -f "$frame"
else
    [ -n "$cam" ] || fail "frame capture skipped: no camera"
    have ffmpeg || fail "frame capture skipped: ffmpeg not installed"
fi

# --- PipeWire ------------------------------------------------------------
if have wpctl && wpctl status >/dev/null 2>&1; then
    pass "PipeWire running (wpctl status)"
else
    fail "PipeWire not reachable (wpctl status) — run inside the desktop login session"
fi

# --- C922 mic source ------------------------------------------------------
if have wpctl && wpctl status 2>/dev/null | grep -qi "C922"; then
    pass "C922 microphone source present in PipeWire"
else
    fail "C922 microphone source not in wpctl status (check the USB cable / default source)"
fi

# --- 2 s recording, non-silent --------------------------------------------
mic="${FRITZ_MIC:-pipewire}"
if have arecord; then
    wav=$(mktemp /tmp/fritz-mic-XXXXXX.wav)
    rec_err=$(arecord -D "$mic" -f S16_LE -r 16000 -c 1 -d 2 -q "$wav" 2>&1)
    rec_status=$?
    if [ "$rec_status" -eq 0 ] && [ -s "$wav" ]; then
        # Max |sample| over the WAV data; silence stays near zero.
        peak=$(python3 - "$wav" <<'PY' 2>/dev/null || echo 0
import struct, sys
data = open(sys.argv[1], "rb").read()
i = data.find(b"data")
if i < 0:
    print(0); raise SystemExit
raw = data[i + 8:]
n = len(raw) // 2
samples = struct.unpack("<%dh" % n, raw[: n * 2]) if n else (0,)
print(max((abs(s) for s in samples), default=0))
PY
)
        if [ "${peak:-0}" -gt 200 ] 2>/dev/null; then
            pass "2 s recording is non-silent (peak amplitude $peak) — make noise during this check"
        else
            fail "2 s recording is silent (peak amplitude ${peak:-0}); speak during the check, check mic level (wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 1.0)"
        fi
    elif [ "$mic" = "pipewire" ] && ! arecord -L 2>/dev/null | grep -qi pipewire; then
        fail "arecord has no pipewire device — install the ALSA bridge: sudo apt install -y pipewire-alsa (then arecord -L | grep pipewire should list it). arecord said: ${rec_err:-no output}"
    else
        fail "arecord -D $mic failed: ${rec_err:-no error output} (check FRITZ_MIC / arecord -L; is another process holding the mic?)"
    fi
    rm -f "$wav"
else
    fail "recording check skipped: arecord not installed (alsa-utils)"
fi

# --- Echo Dot speaker ------------------------------------------------------
# If the MAC isn't configured, try to discover a paired Echo so the
# check still runs; the fix in that case is to persist the found MAC.
if [ -z "${FRITZ_SPEAKER_BT:-}" ] && have bluetoothctl; then
    discovered=$(bluetoothctl devices Paired 2>/dev/null | grep -iE "echo|amazon" | head -n 1 | awk '{print $2}')
    if [ -n "$discovered" ]; then
        FRITZ_SPEAKER_BT="$discovered"
        echo "note: FRITZ_SPEAKER_BT unset; using paired Echo $FRITZ_SPEAKER_BT — persist it with: echo 'FRITZ_SPEAKER_BT=$FRITZ_SPEAKER_BT' | sudo tee -a /etc/fritz/fritz.env"
    fi
fi
if [ -n "${FRITZ_SPEAKER_BT:-}" ] && have bluetoothctl; then
    if bluetoothctl info "$FRITZ_SPEAKER_BT" 2>/dev/null | grep -q "Connected: yes"; then
        pass "Echo Dot connected ($FRITZ_SPEAKER_BT)"
    else
        fail "Echo Dot not connected (bluetoothctl connect $FRITZ_SPEAKER_BT)"
    fi
elif [ -z "${FRITZ_SPEAKER_BT:-}" ]; then
    fail "no Echo Dot found — pair it first: bluetoothctl pair <MAC> && bluetoothctl trust <MAC>, then set FRITZ_SPEAKER_BT=<MAC> in /etc/fritz/fritz.env (find <MAC> with: bluetoothctl devices)"
else
    fail "Echo check skipped: bluetoothctl not installed (bluez)"
fi

# wpctl status lists sinks by description ("Echo Dot-8SG"); the
# bluez_output.* node name only shows there once it is the configured
# default, so look the node names up directly with pw-cli.
if have pw-cli && pw-cli ls Node 2>/dev/null | grep -q 'node.name = "bluez_output'; then
    pass "Echo Dot PipeWire sink present (bluez_output.*)"
elif have wpctl && wpctl status 2>/dev/null | grep -q "bluez_output"; then
    pass "Echo Dot PipeWire sink present (bluez_output.*)"
else
    fail "no bluez_output sink in wpctl status (pair/trust the Echo, wpctl set-default)"
fi

echo
if [ "$failures" -eq 0 ]; then
    echo "All hardware checks passed."
else
    echo "$failures check(s) FAILED."
    exit 1
fi
