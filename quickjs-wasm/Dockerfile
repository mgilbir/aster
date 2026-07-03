FROM debian:bookworm-slim AS build

ARG TARGETARCH
ARG QUICKJS_NG_VERSION=v0.15.1
ARG WASI_SDK_VERSION=29
ARG BINARYEN_VERSION=122

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl cmake make patch \
    && rm -rf /var/lib/apt/lists/*

# wasi-sdk (same toolchain quickjs-ng CI uses)
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x86_64) && \
    curl -sSL "https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION}/wasi-sdk-${WASI_SDK_VERSION}.0-${ARCH}-linux.tar.gz" \
    | tar xz -C /opt && \
    mv /opt/wasi-sdk-${WASI_SDK_VERSION}.0-*-linux /opt/wasi-sdk

# binaryen for wasm-opt
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo aarch64 || echo x86_64) && \
    curl -sSL "https://github.com/WebAssembly/binaryen/releases/download/version_${BINARYEN_VERSION}/binaryen-version_${BINARYEN_VERSION}-${ARCH}-linux.tar.gz" \
    | tar xz -C /opt && \
    mv /opt/binaryen-version_${BINARYEN_VERSION} /opt/binaryen

# quickjs-ng source at the pinned release tag
WORKDIR /src
RUN curl -sSL "https://github.com/quickjs-ng/quickjs/archive/refs/tags/${QUICKJS_NG_VERSION}.tar.gz" \
    | tar xz --strip-components=1

# Re-enable the JS stack-overflow guard on WASI: upstream hard-disables it
# (update_stack_limit forces stack_limit = 0), so deep recursion overflows
# the WASM stack instead of raising a catchable RangeError. The guard's
# shadow-stack-pointer measurement works fine under wazero.
COPY stack-guard.patch /src/
RUN patch -p1 < stack-guard.patch

# Reactor build like upstream CI, plus LTO. CMake Release already means
# -O3 -DNDEBUG; LTO + wasm-opt below are what upstream's release artifact
# lacks and what the interpreter loop benefits from most.
# The linker flags diverge from upstream deliberately: Vega recursion is
# deeper than the 64KB default stack, and --stack-first makes an overflow
# trap cleanly instead of silently corrupting globals/heap. The Go host
# sets the engine's JS stack limit below this so deep recursion surfaces
# as a RangeError before the hard trap.
RUN cmake -B build \
    -DCMAKE_TOOLCHAIN_FILE=/opt/wasi-sdk/share/cmake/wasi-sdk.cmake \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_INTERPROCEDURAL_OPTIMIZATION=ON \
    -DCMAKE_EXE_LINKER_FLAGS="-Wl,-z,stack-size=8388608 -Wl,--stack-first" \
    -DQJS_WASI_REACTOR=ON \
    && make -C build qjs_wasi -j"$(nproc)"

RUN /opt/binaryen/bin/wasm-opt -O3 \
    --enable-bulk-memory --enable-nontrapping-float-to-int --enable-sign-ext \
    -o /src/quickjs-opt.wasm /src/build/qjs.wasm && \
    ls -la /src/build/qjs.wasm /src/quickjs-opt.wasm

FROM scratch
COPY --from=build /src/quickjs-opt.wasm /output/quickjs.wasm
