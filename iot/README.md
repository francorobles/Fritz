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

## Build on macOS/any host (logic check only)

Same CMake commands as on the Pi — the code is portable C++17 and builds
without GPIO. Enable Pi GPIO code paths with
`-DFRITZ_IOT_ENABLE_GPIO=ON`.

## Layout

- `src/main.cpp` — entry point, signal handling, heartbeat loop
- `include/fritz/device.hpp`, `src/device.cpp` — device identity/status
