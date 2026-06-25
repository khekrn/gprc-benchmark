#!/usr/bin/env bash
# Build the go-gorm gRPC server (Go + GORM over jackc/pgx).
# Generates the Go protobuf/gRPC stubs from the shared proto first, then builds.
#
# Requires: go (1.23+), protoc. protoc-gen-go plugins are installed via
# `go install` into $(go env GOPATH)/bin, added to PATH here.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

GORM_DIR="${ROOT_DIR}/go-gorm"
GEN_DIR="${GORM_DIR}/gen/benchv1"

command -v go >/dev/null || { echo "go not found in PATH"; exit 1; }
command -v protoc >/dev/null || { echo "protoc not found in PATH"; exit 1; }

export PATH="$PATH:$(go env GOPATH)/bin"

echo ">> Installing protoc-gen-go and protoc-gen-go-grpc"
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

echo ">> Generating Go stubs from proto"
mkdir -p "${GEN_DIR}"
TMP_GEN="$(mktemp -d)"
protoc \
  --proto_path="${ROOT_DIR}/proto" \
  --go_out="${TMP_GEN}" --go_opt=paths=source_relative \
  --go-grpc_out="${TMP_GEN}" --go-grpc_opt=paths=source_relative \
  "${ROOT_DIR}/proto/command.proto"
cp "${TMP_GEN}"/command.pb.go "${GEN_DIR}/command.pb.go"
cp "${TMP_GEN}"/command_grpc.pb.go "${GEN_DIR}/command_grpc.pb.go"
rm -rf "${TMP_GEN}"

echo ">> Building go-gorm server (go mod tidy + build)"
( cd "${GORM_DIR}" && go mod tidy && go build -o "${ROOT_DIR}/bin/go-gorm-server" . )

echo ">> Done. Binary at ${ROOT_DIR}/bin/go-gorm-server"
