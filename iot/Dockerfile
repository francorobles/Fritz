# Fritz IoT — Raspberry Pi 4B (64-bit Raspberry Pi OS, linux/arm64)
#
# Build for the Pi from this (x86_64) host:
#   docker buildx build --platform linux/arm64 -t fritz-iot:latest --load .
# Ship to the Pi without a registry:
#   docker save fritz-iot:latest | gzip > fritz-iot.tar.gz
#   scp fritz-iot.tar.gz pi@<pi-host>:~/
#   ssh pi@<pi-host> 'docker load < ~/fritz-iot.tar.gz'
#
# The builder runs linux/arm64 (QEMU-emulated on x86_64, native on the Pi),
# so SDL2 and its headers/libs always match the target. Slower than a
# cross-compile, but no multiarch sysroot to maintain. Multi-stage: the
# runtime image carries only the binary and SDL2 runtime libraries.

# ---- Builder ----
FROM debian:bookworm-slim AS builder

ARG FRITZ_IOT_ENABLE_GPIO=OFF

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      cmake \
      libsdl2-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY CMakeLists.txt ./
COPY cmake ./cmake
COPY include ./include
COPY src ./src

# BUILD_TESTING=OFF: tests/ is not copied into this image (it only needs
# the fritz_iot binary); without this, configure fails on the missing
# tests/ sources.
RUN cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
      -DBUILD_TESTING=OFF \
      -DFRITZ_IOT_ENABLE_GPIO=$FRITZ_IOT_ENABLE_GPIO \
 && cmake --build build -j"$(nproc)" \
 && cmake --install build --prefix /out

# ---- Runtime ----
FROM debian:bookworm-slim AS runtime

# libsdl2-2.0-0: SDL itself. libegl1/libgles2/libgbm1/libgl1-mesa-dri:
# GPU rendering on the desktop (GL) and in kiosk mode (KMSDRM/GBM).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libsdl2-2.0-0 \
      libegl1 \
      libgles2 \
      libgbm1 \
      libgl1-mesa-dri \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --system --no-create-home --shell /usr/sbin/nologin fritz \
 && groupadd -f render \
 && groupadd -f input \
 && usermod -a -G video,render,input fritz
COPY --from=builder /out/bin/fritz_iot /usr/local/bin/fritz_iot

USER fritz
ENTRYPOINT ["/usr/local/bin/fritz_iot"]
