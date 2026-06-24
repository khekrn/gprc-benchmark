# --- Build stage --------------------------------------------------------
FROM rust:1 AS build
WORKDIR /src

# Copy manifests first so the dependency build layer caches.
COPY Cargo.toml Cargo.lock ./
COPY build.rs ./
COPY proto ./proto
# A stub lib so `cargo build` can compile dependencies without the full source.
RUN mkdir src && echo "" > src/lib.rs && echo "fn main() {}" > src/main.rs \
    && cargo build --release || true

# Now the real sources.
COPY migrations ./migrations
COPY config.yaml ./config.yaml
COPY src ./src
RUN touch src/main.rs src/lib.rs && cargo build --release

# --- Runtime stage ------------------------------------------------------
# `cc` image: the binary links glibc (tokio-postgres, no static musl build here).
FROM gcr.io/distroless/cc-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/khekrn/gprc-benchmark"

COPY --from=build /src/target/release/users-rust /users-rust
COPY --from=build /src/config.yaml /config.yaml

EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/users-rust"]
