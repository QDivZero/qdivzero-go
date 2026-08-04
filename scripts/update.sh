#!/usr/bin/env bash
# Regenerates the qdivzero-go client from the live OpenAPI spec and publishes
# a new v1.0.<n> tag when the spec changed. Runs locally or in CI.
set -euo pipefail
cd "$(dirname "$0")/.."

SPEC_URL="${SPEC_URL:-https://api.qdiv0.com/openapi}"
OAPI_VERSION="${OAPI_VERSION:-v2.8.0}"

RAW="$(mktemp)"
CLEAN="$(mktemp)"
trap 'rm -f "$RAW" "$CLEAN"' EXIT

echo "==> fetching spec: $SPEC_URL"
curl -fsSL "$SPEC_URL" -o "$RAW"

if cmp -s "$RAW" api/openapi.json; then
  echo "==> spec unchanged, nothing to do"
  exit 0
fi

echo "==> stripping x-go-name extensions"
python3 - "$RAW" "$CLEAN" <<'EOF'
import json, sys
spec = json.load(open(sys.argv[1]))
for sch in spec.get('components', {}).get('schemas', {}).values():
    sch.pop('x-go-name', None)
json.dump(spec, open(sys.argv[2], 'w'))
EOF

export PATH="$PATH:$(go env GOPATH)/bin"
if ! command -v oapi-codegen >/dev/null 2>&1; then
  echo "==> installing oapi-codegen $OAPI_VERSION"
  go install "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$OAPI_VERSION"
fi

echo "==> regenerating client"
oapi-codegen --config api/gen-types.yaml "$CLEAN"
oapi-codegen --config api/gen-client.yaml "$CLEAN"
go mod tidy

echo "==> verifying"
gofmt -l .
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go build ./...

HASH="$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest()[:12])' "$RAW")"
LATEST="$(git tag --sort=-v:refname | head -n1 || true)"
if [ -z "$LATEST" ]; then
  NEXT="v1.0.1"
else
  BASE="${LATEST#v}"
  REST="${BASE#*.}"
  PATCH="${REST#*.}"
  NEXT="v1.0.$((PATCH + 1))"
fi

echo "==> committing and tagging $NEXT"
cp "$RAW" api/openapi.json
git config user.name "${GIT_AUTHOR_NAME:-github-actions[bot]}"
git config user.email "${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}"
git add api/openapi.json types.gen.go client.gen.go go.mod go.sum
git commit -m "chore: regenerate from OpenAPI spec (hash $HASH)"
git push origin "HEAD:main"
git tag "$NEXT"
git push origin "$NEXT"
echo "==> released $NEXT"
