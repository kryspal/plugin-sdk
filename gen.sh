#!/usr/bin/env bash
# Regenerate pluginv1/*.pb.go. The outputs are committed; only run this after editing a .proto.
#
# Pinned generator versions (the generated headers record them too):
#   protoc              36.2      https://github.com/protocolbuffers/protobuf/releases/tag/v36.2
#   protoc-gen-go       v1.36.12  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
#   protoc-gen-go-grpc  v1.6.2    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
#
# Usage: ./gen.sh   (PROTOC selects the protoc binary; both Go plugins must be on PATH.)
set -euo pipefail

want_protoc="libprotoc 36.2"
want_go="protoc-gen-go v1.36.12"
want_grpc="protoc-gen-go-grpc 1.6.2"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
protoc="${PROTOC:-protoc}"

check() {
  local got="$1" want="$2"
  if [[ "$got" != "$want" ]]; then
    if [[ "${GEN_ALLOW_VERSION_MISMATCH:-}" == "1" ]]; then
      echo "warning: got '$got', want '$want'" >&2
    else
      echo "error: got '$got', want '$want' (set GEN_ALLOW_VERSION_MISMATCH=1 to override)" >&2
      exit 1
    fi
  fi
}

check "$("$protoc" --version)" "$want_protoc"
check "$(protoc-gen-go --version)" "$want_go"
check "$(protoc-gen-go-grpc --version)" "$want_grpc"

cd "$root"
"$protoc" \
  --proto_path=. \
  --go_out=. --go_opt=module=github.com/kryspal/plugin-sdk \
  --go-grpc_out=. --go-grpc_opt=module=github.com/kryspal/plugin-sdk \
  pluginv1/*.proto

# Guard: the generated module must build and vet cleanly (a proto left out above would leave the SDK uncompilable).
go build ./... && go vet ./...

echo "generated pluginv1/*.pb.go"
