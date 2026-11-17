#!/usr/bin/env bash
# Build signed agent release artifacts (ADR-0010 D1). Used by the release
# workflow and by the CI dry run.
#   scripts/build_agent_release.sh OUTDIR
# Environment: DECKBEACON_RELEASE_KEY (PEM, required to sign),
#              VERIFY_PUB (optional: verify with this key instead of the
#              keys built into the agent).
set -euo pipefail
out="${1:?usage: build_agent_release.sh OUTDIR}"
mkdir -p "$out"
out="$(cd "$out" && pwd)" # absolute: the builds run from apps/agent
root="$(cd "$(dirname "$0")/.." && pwd)"
version="$(tr -d '[:space:]' < "$root/apps/agent/VERSION")"
commit="$(git -C "$root" rev-parse --short HEAD)"
min_integration="$(tr -d '[:space:]' < "$root/apps/agent/MIN_INTEGRATION")"
pkg=github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo
for arch in amd64 arm64; do
  (cd "$root/apps/agent" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit" \
    -o "$out/deckbeacon-linux-$arch" ./cmd/deckbeacon)
done
tool="$out/deckbeacon-linux-$(go env GOARCH)"
"$tool" version | grep -q " $version " || { echo "built binary does not report $version"; exit 1; }
"$tool" release manifest --version "$version" --min-integration "$min_integration" \
  --notes "https://github.com/Jafesu/DeckBeacon/releases/tag/agent/v$version" \
  deckbeacon-linux-amd64="$out/deckbeacon-linux-amd64" deckbeacon-linux-arm64="$out/deckbeacon-linux-arm64" > "$out/release.json"
"$tool" release sign --in "$out/release.json" --out "$out/release.json.sig"
verify=()
[ -n "${VERIFY_PUB:-}" ] && verify=(--pub "$VERIFY_PUB")
# Without VERIFY_PUB this proves the signing key matches a key built into the agent.
"$tool" release verify "${verify[@]}" --in "$out/release.json" --sig "$out/release.json.sig" \
  deckbeacon-linux-amd64="$out/deckbeacon-linux-amd64" deckbeacon-linux-arm64="$out/deckbeacon-linux-arm64"
echo "$version"
