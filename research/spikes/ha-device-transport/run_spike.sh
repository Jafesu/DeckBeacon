#!/usr/bin/env bash
# Milestone 0 spike runner. Usage: run_spike.sh <python-with-homeassistant> <workdir>
set -euo pipefail
PY="$1"; WORK="$2"; HERE="$(cd "$(dirname "$0")" && pwd)"
rm -rf "$WORK"; mkdir -p "$WORK/config/custom_components"
cp -r "$HERE/custom_components/deckbeacon_spike" "$WORK/config/custom_components/"
(cd "$HERE/client" && go build -o "$WORK/client" .)
# Generate a device key (good) and an unenrolled key (bad).
"$PY" -I -c "
import base64,os
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization as s
for n in ('good','bad'):
    seed=os.urandom(32); k=Ed25519PrivateKey.from_private_bytes(seed)
    open('$WORK/'+n+'.seed','w').write(base64.b64encode(seed).decode())
    open('$WORK/'+n+'.pub','w').write(base64.b64encode(k.public_key().public_bytes(s.Encoding.Raw,s.PublicFormat.Raw)).decode())
"
cat > "$WORK/config/configuration.yaml" <<YAML
http:
  server_host: 127.0.0.1
  server_port: 18123
deckbeacon_spike:
  devices:
    deck-test-1: "$(cat "$WORK/good.pub")"
YAML
"$PY" -m homeassistant -c "$WORK/config" >"$WORK/hass.log" 2>&1 &
HPID=$!; trap 'kill $HPID 2>/dev/null || true' EXIT
for i in $(seq 1 400); do curl -s -o /dev/null http://127.0.0.1:18123/ && break; sleep 1; done
URL=ws://127.0.0.1:18123/api/deckbeacon_spike/ws
echo "== T1 enrolled device key"; "$WORK/client" -url $URL -key "$WORK/good.seed" -device deck-test-1
echo "== T2 unenrolled key (expect rejection, exit 3)"; set +e; "$WORK/client" -url $URL -key "$WORK/bad.seed" -device deck-test-1; echo "exit=$?"
echo "== T3 HA REST API without token (expect 401)"; curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:18123/api/states
echo "== T4 HA websocket API is NOT reachable with device key (device holds no HA token)"; "$WORK/client" -url ws://127.0.0.1:18123/api/websocket -key "$WORK/good.seed" -device deck-test-1; echo "exit=$?"; set -e
grep -i "deckbeacon_spike" "$WORK/hass.log" | grep -v "custom integration" | tail -3 || true
