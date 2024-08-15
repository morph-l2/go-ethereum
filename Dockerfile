# Support setting various labels on the final image
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""
ARG MORPH_LIB_PATH=/morph/lib

# Build libzkp dependency
FROM scrolltech/go-rust-builder:go-1.21-rust-nightly-2023-12-03 as chef
WORKDIR app

FROM chef as planner
COPY ./rollup/circuitcapacitychecker/libzkp/ .
RUN cargo chef prepare --recipe-path recipe.json

FROM chef as zkp-builder
COPY ./rollup/circuitcapacitychecker/libzkp/rust-toolchain ./
COPY --from=planner /app/recipe.json recipe.json
RUN cargo chef cook --release --recipe-path recipe.json

COPY ./rollup/circuitcapacitychecker/libzkp .
RUN cargo clean
RUN cargo build --release

# Build Geth in a stock Go builder container
FROM scrolltech/go-rust-builder:go-1.21-rust-nightly-2023-12-03 as builder

ADD . /go-ethereum

ARG MORPH_LIB_PATH

RUN mkdir -p $MORPH_LIB_PATH

COPY --from=zkp-builder /app/target/release/libzkp.so $MORPH_LIB_PATH

ENV LD_LIBRARY_PATH=$LD_LIBRARY_PATH:$MORPH_LIB_PATH
ENV CGO_LDFLAGS="-L$MORPH_LIB_PATH -Wl,-rpath,$MORPH_LIB_PATH"

RUN cd /go-ethereum && env GO111MODULE=on go run build/ci.go install -buildtags circuit_capacity_checker ./cmd/geth

# Pull Geth into a second stage deploy alpine container
FROM ubuntu:20.04

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends ca-certificates

COPY --from=builder /go-ethereum/build/bin/geth /usr/local/bin/

ARG MORPH_LIB_PATH

RUN mkdir -p $MORPH_LIB_PATH

COPY --from=zkp-builder /app/target/release/libzkp.so $MORPH_LIB_PATH

ENV LD_LIBRARY_PATH=$LD_LIBRARY_PATH:$MORPH_LIB_PATH
ENV CGO_LDFLAGS="-ldl -L$MORPH_LIB_PATH -Wl,-rpath,$MORPH_LIB_PATH"

EXPOSE 8545 8546 30303 30303/udp
ENTRYPOINT ["geth"]

# Add some metadata labels to help programatic image consumption
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

LABEL commit="$COMMIT" version="$VERSION" buildnum="$BUILDNUM"