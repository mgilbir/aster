FROM rust:latest AS build

RUN rustup target add wasm32-wasip1

WORKDIR /src
COPY Cargo.toml Cargo.toml
COPY src/ src/

RUN cargo build --target wasm32-wasip1 --release

# Install recent binaryen from GitHub (distro version is too old for bulk-memory ops)
RUN BINARYEN_VERSION=122 && \
    curl -sSL "https://github.com/WebAssembly/binaryen/releases/download/version_${BINARYEN_VERSION}/binaryen-version_${BINARYEN_VERSION}-x86_64-linux.tar.gz" \
    | tar xz -C /opt && \
    /opt/binaryen-version_${BINARYEN_VERSION}/bin/wasm-opt -Os \
        --enable-bulk-memory --enable-nontrapping-float-to-int \
        -o /src/target/wasm32-wasip1/release/resvg_wasm_opt.wasm \
        /src/target/wasm32-wasip1/release/resvg_wasm.wasm

FROM scratch
COPY --from=build /src/target/wasm32-wasip1/release/resvg_wasm_opt.wasm /output/resvg.wasm
