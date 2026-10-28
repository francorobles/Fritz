# Fritz IoT — Raspberry Pi 4B (64-bit Raspberry Pi OS, linux/arm64)
#
# Build for the Pi from this (x86_64) host:
#   docker buildx build --platform linux/arm64 -t fritz-iot:latest --load .
# Ship to the Pi without a registry:
#   docker save fritz-iot:latest | gzip > fritz-iot.tar.gz
#   scp fritz-iot.tar.gz pi@<pi-host>:~/
#   ssh pi@<pi-host> 'docker load < ~/fritz-iot.tar.gz'
#
# Multi-stage: the runtime image carries only the binary, no toolchain.

# ---- Builder ----
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS builder

ARG TARGETARCH
ARG FRITZ_IOT_ENABLE_GPIO=OFF

RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential cmake \
 && if [ "$(dpkg --print-architecture)" != "$TARGETARCH" ]; then \
      apt-get install -y --no-install-recommends crossbuild-essential-arm64; \
    fi \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY CMakeLists.txt ./
COPY cmake ./cmake
COPY include ./include
COPY src ./src

# Native build when the builder arch matches the target (e.g. building on
# the Pi itself); cross-compile with the repo toolchain otherwise.
RUN if [ "$(dpkg --print-architecture)" = "$TARGETARCH" ]; then \
      cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
        -DFRITZ_IOT_ENABLE_GPIO=$FRITZ_IOT_ENABLE_GPIO; \
    else \
      cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_TOOLCHAIN_FILE=cmake/aarch64-linux-gnu.toolchain.cmake \
        -DFRITZ_IOT_ENABLE_GPIO=$FRITZ_IOT_ENABLE_GPIO; \
    fi \
 && cmake --build build -j"$(nproc)" \
 && cmake --install build --prefix /out

# ---- Runtime ----
FROM debian:bookworm-slim AS runtime

RUN useradd --system --no-create-home --shell /usr/sbin/nologin fritz
COPY --from=builder /out/bin/fritz_iot /usr/local/bin/fritz_iot

USER fritz
ENTRYPOINT ["/usr/local/bin/fritz_iot"]
