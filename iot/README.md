# Fritz — IoT (C++ / Raspberry Pi 4B)

C++17 device program for a Raspberry Pi 4B. Opens a fullscreen SDL2
window showing a pair of EVE-pattern eyes on black: rounded striped
ovals. All expressions use the same blue color, fine scanlines, and soft
halo as the normal eyes, matching the EVE reference. Each expression keeps
its own tilt, gaze, and openness. All nine expressions from
[easydrawingtips.com's eye-expressions tutorial](https://www.easydrawingtips.com/how-to-draw-eye-expressions-step-by-step/)
are implemented: normal, thinking, surprised, angry, skeptical, focused,
scared, happy, tired. At startup the app is in standby — it alternates
between normal and thinking eyes and blinks like a human. Works on the
Pi OS desktop and in kiosk mode (no desktop, SDL KMSDRM). SDL2 is the
only dependency. GPIO access is behind the
`FRITZ_IOT_ENABLE_GPIO` build flag until a backend (e.g. libgpiod) is
chosen.

## Install SDL2

```sh
# Raspberry Pi OS / Debian
sudo apt install libsdl2-dev
# Omarchy / Arch
sudo pacman -S sdl2-compat   # provides the SDL2 API
```

## Build & run

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
./build/fritz_iot             # fullscreen standby; Esc or Q quits
./build/fritz_iot --windowed  # development on this desktop
./build/fritz_iot --windowed --emotion angry   # preview one expression
./build/fritz_iot --help      # lists all nine emotion names
```

On the Pi, fullscreen is the default either way — over the desktop, or
directly on the display when booted to console (kiosk user needs the
`video`, `render`, and `input` groups for `/dev/dri` and `/dev/input`).

## Cross-compile from a desktop (64-bit Raspberry Pi OS)

Needs an arm64 SDL2 sysroot, so the Docker build below is the supported
path for desktop-to-Pi builds. The toolchain file remains for a sysroot
setup:

```sh
# requires: g++-aarch64-linux-gnu + arm64 SDL2 in the sysroot
cmake -S . -B build-pi \
  -DCMAKE_TOOLCHAIN_FILE=cmake/aarch64-linux-gnu.toolchain.cmake \
  -DCMAKE_BUILD_TYPE=Release
cmake --build build-pi
scp build-pi/fritz_iot pi@<pi-host>:~/
```

## Docker (build here, run on the Pi)

Daily development stays native (above). Docker is the packaging step:
it produces a `linux/arm64` image matching 64-bit Raspberry Pi OS.

```sh
# One-time: register QEMU for arm64 emulation and create a buildx builder
docker run --privileged --rm tonistiigi/binfmt --install arm64
docker buildx create --name fritz-builder --use

# Build the arm64 image on this host
docker buildx build --platform linux/arm64 -t fritz-iot:latest --load .
```

Ship it to the Pi without a registry:

```sh
docker save fritz-iot:latest | gzip > fritz-iot.tar.gz
scp fritz-iot.tar.gz pi@<pi-host>:~/
ssh pi@<pi-host> 'docker load < ~/fritz-iot.tar.gz'
```

Run it on the Pi:

```sh
# On the Pi, with this whole iot/ folder copied over (compose passes the
# display, /dev/dri, and /dev/input into the container):
docker compose up -d                     # detached, restart: unless-stopped
docker compose logs -f
```

Building directly on the Pi (`docker compose up -d --build`) also works,
it is just slower — the Docker builder compiles under QEMU on a desktop
host for the same reason. GPIO passthrough (`/dev/gpiomem`) is stubbed
as comments in `docker-compose.yml` until a GPIO backend is chosen.

## Run natively on the Pi (systemd, alternative to Docker)

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release && cmake --build build
sudo install -m755 build/fritz_iot /opt/fritz/fritz_iot
sudo cp systemd/fritz-face.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now fritz-face
journalctl -u fritz-face -f   # logs
```

The unit in `systemd/fritz-face.service` runs the face as the desktop
login user `franco` (uid 1000; edit `User=`/`XDG_RUNTIME_DIR` if yours
differs — `id -un`, `id -u`) so it shares the desktop and PipeWire
session with the Go brain (desktop mode via `DISPLAY=:0`; delete those lines for KMSDRM kiosk —
details in the unit's comments). The Go brain gets the matching
`fritz.service` from the fritz-pi-deploy skill.

## Build on macOS/any host (logic check only)

Same CMake commands as on the Pi, plus the SDL2 dev package for your
platform — the code is portable C++17. Run `./build/fritz_iot --windowed`
to check it without taking over the screen. Enable Pi GPIO code paths
with `-DFRITZ_IOT_ENABLE_GPIO=ON`.

## Face protocol (UDP)

The Go brain drives the face over UDP on `127.0.0.1:7070` (one ASCII
command per datagram; `--face-port N` changes it, `0` disables it):

```
emotion <name>   normal thinking surprised angry skeptical focused
                 scared happy tired — pins an expression, leaves standby
standby          back to the idle Normal/Thinking cycle
gaze <x> <y>     look toward (x, y) in [-1, 1]; eased, expires in 1.5 s
gaze off         drop the gaze override
speaking on|off  subtle pulse while TTS plays
blink            blink once now
```

Unknown commands are logged and ignored. Try it while the face runs:

```sh
echo "emotion happy" | nc -u -w0 127.0.0.1 7070
```

Parser and loopback tests: `ctest --test-dir build` (or run
`build/fritz_face_test` directly).

## Layout

- `src/main.cpp` — entry point, CLI args, signal handling
- `include/fritz/app.hpp`, `src/app.cpp` — SDL2 fullscreen window + loop;
  polls the face server each frame before `eyes_.update()`
- `include/fritz/eyes.hpp`, `src/eyes.cpp` — the eyes; all nine `Emotion`
  expressions live in the `shape_for` table. `Eyes::update()` runs human
  blinking (every emotion) plus standby Normal/Thinking cycling and gaze
- `include/fritz/face_server.hpp`, `src/face_server.cpp` — non-blocking
  UDP face-protocol listener and command parser (no threads)
- `include/fritz/device.hpp`, `src/device.cpp` — device identity/status
- `systemd/fritz-face.service` — native systemd unit for the Pi
- `Dockerfile` — multi-stage arm64 build (builder + slim SDL2 runtime)
- `docker-compose.yml` — run on the Pi (`docker compose up -d`)
