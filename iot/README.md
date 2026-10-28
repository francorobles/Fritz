# Fritz — IoT (C++ / Raspberry Pi 4B)

C++17 device program for a Raspberry Pi 4B. No third-party dependencies yet;
GPIO access is behind the `FRITZ_IOT_ENABLE_GPIO` build flag until a backend
(e.g. libgpiod) is chosen.

## Build & run on the Pi

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
./build/fritz_iot        # prints status + heartbeat; Ctrl-C to stop
```

## Cross-compile from a desktop (64-bit Raspberry Pi OS)

```sh
# requires: g++-aarch64-linux-gnu
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
docker run --rm fritz-iot:latest        # quick check: status + heartbeat
docker run -d --restart unless-stopped --name fritz-iot fritz-iot:latest
docker logs -f fritz-iot
# or copy this whole iot/ folder to the Pi and use compose instead:
docker compose up -d                     # detached, restart: unless-stopped
docker compose logs -f
```

Building directly on the Pi (`docker compose up -d --build`) also works,
it is just slower. GPIO passthrough (`/dev/gpiomem`) is stubbed as comments
in `docker-compose.yml` until a GPIO backend is chosen.

## Build on macOS/any host (logic check only)

Same CMake commands as on the Pi — the code is portable C++17 and builds
without GPIO. Enable Pi GPIO code paths with
`-DFRITZ_IOT_ENABLE_GPIO=ON`.

## Layout

- `src/main.cpp` — entry point, signal handling, heartbeat loop
- `include/fritz/device.hpp`, `src/device.cpp` — device identity/status
- `Dockerfile` — multi-stage arm64 build (builder + slim runtime)
- `docker-compose.yml` — run on the Pi (`docker compose up -d`)
