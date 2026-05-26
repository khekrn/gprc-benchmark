#!/usr/bin/env bash
# Build the Rust/tonic + tokio-postgres + deadpool server.
# Outputs bin/rust-server. tonic-build (in build.rs) needs `protoc` on PATH.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

RUST_DIR="${ROOT_DIR}/rust-tokio"

command -v cargo  >/dev/null || { echo "cargo not found in PATH";  exit 1; }
command -v protoc >/dev/null || { echo "protoc not found in PATH"; exit 1; }

echo ">> Building Rust server (cargo build --release)"
( cd "${RUST_DIR}" && cargo build --release )

mkdir -p "${ROOT_DIR}/bin"
cp "${RUST_DIR}/target/release/rust-tokio-bench" "${ROOT_DIR}/bin/rust-server"
echo ">> Done. Binary at ${ROOT_DIR}/bin/rust-server"
