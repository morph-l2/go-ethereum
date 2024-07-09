# Support setting various labels on the final image
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

# Build libzkp dependency
FROM scrolltech/go-rust-builder:go-1.21-rust-nightly-2023-12-03 as chef
WORKDIR app

FROM chef as planner
COPY ./rollup/circuitcapacitychecker/libzkp/ .
RUN cargo chef prepare --recipe-path recipe.json

FROM chef as zkp-builder
COPY ./rollup/circuitcapacitychecker/libzkp/rust-toolchain ./
COPY --from=planner /app/recipe.json recipe.json
ENV RUST_BACKTRACE=full
RUN cargo chef cook --release --recipe-path recipe.json

COPY ./rollup/circuitcapacitychecker/libzkp .
RUN cargo clean
RUN cargo build --release
RUN find ./ | grep libzktrie.so | xargs -I{} cp {} /app/target/release/

# Build Geth in a stock Go builder container
FROM scrolltech/go-rust-builder:go-1.21-rust-nightly-2023-12-03 as builder

ADD . /go-ethereum
COPY --from=zkp-builder /app/target/release/libzkp.so /usr/local/lib/
COPY --from=zkp-builder /app/target/release/libzktrie.so /usr/local/lib/
ENV LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/usr/local/lib/
RUN cd /go-ethereum && env GO111MODULE=on go run build/ci.go install -buildtags circuit_capacity_checker ./cmd/geth

# Pull Geth into a second stage deploy alpine container
FROM ubuntu:20.04

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends ca-certificates

COPY entrypoint.sh /entrypoint.sh
COPY --from=builder /go-ethereum/build/bin/geth /usr/local/bin/
COPY --from=zkp-builder /app/target/release/libzkp.so /usr/local/lib/
COPY --from=zkp-builder /app/target/release/libzktrie.so /usr/local/lib/
ENV LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/usr/local/lib/

VOLUME ["/db"]

ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]

EXPOSE 8545 8546 8551 30303 30303/udp

# Add some metadata labels to help programatic image consumption
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

LABEL commit="$COMMIT" version="$VERSION" buildnum="$BUILDNUM"

