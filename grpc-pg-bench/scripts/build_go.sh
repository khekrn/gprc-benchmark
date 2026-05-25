#!/usr/bin/env bash
# Build the Go gRPC server and the shared load generator.
# Generates Go protobuf/gRPC stubs from the shared proto first.
#
# Requires: go (1.23+), protoc. The protoc-gen-go plugins are installed via
# `go install` into $(go env GOPATH)/bin, which is added to PATH here.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

GO_DIR="${ROOT_DIR}/go-pgx"
LOADGEN_DIR="${ROOT_DIR}/loadgen"
GEN_DIR="${GO_DIR}/gen/benchv1"

command -v go >/dev/null || { echo "go not found in PATH"; exit 1; }
command -v protoc >/dev/null || { echo "protoc not found in PATH"; exit 1; }

export PATH="$PATH:$(go env GOPATH)/bin"

echo ">> Installing protoc-gen-go and protoc-gen-go-grpc"
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

echo ">> Generating Go stubs from proto"
mkdir -p "${GEN_DIR}"
# Generate with source_relative paths into a temp dir, then place the two
# files into gen/benchv1 (the package dir the code imports).
TMP_GEN="$(mktemp -d)"
protoc \
  --proto_path="${ROOT_DIR}/proto" \
  --go_out="${TMP_GEN}" --go_opt=paths=source_relative \
  --go-grpc_out="${TMP_GEN}" --go-grpc_opt=paths=source_relative \
  "${ROOT_DIR}/proto/command.proto"
cp "${TMP_GEN}"/command.pb.go "${GEN_DIR}/command.pb.go"
cp "${TMP_GEN}"/command_grpc.pb.go "${GEN_DIR}/command_grpc.pb.go"
rm -rf "${TMP_GEN}"

echo ">> Building Go server"
( cd "${GO_DIR}" && go mod tidy && go build -o "${ROOT_DIR}/bin/go-server" . )

echo ">> Building load generator"
( cd "${LOADGEN_DIR}" && go mod tidy && go build -o "${ROOT_DIR}/bin/loadgen" . )

echo ">> Done. Binaries in ${ROOT_DIR}/bin/"
