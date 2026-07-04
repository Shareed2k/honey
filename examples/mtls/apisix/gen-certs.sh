#!/usr/bin/env bash
# Generate a demo PKI for the APISIX mTLS example:
#   ca.crt/ca.key         - the CA APISIX trusts for client certs (and that signs the server cert here)
#   server.crt/server.key - APISIX TLS server cert for SNI "honey.example"
#   client.crt/client.key - a device client cert, subject CN=honey-app
#
# For a real deployment the client cert is issued per-device by honey's device CA
# (Phase 2) and this CA is what you put in the APISIX SSL resource's `client.ca`.
set -euo pipefail

OUT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/certs"
mkdir -p "$OUT_DIR"
cd "$OUT_DIR"

SNI="${SNI:-honey.example}"
CLIENT_CN="${CLIENT_CN:-honey-app}"
DAYS="${DAYS:-825}"

echo "==> CA"
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out ca.key
openssl req -x509 -new -key ca.key -days 3650 -subj "/CN=honey-demo-ca" -out ca.crt

echo "==> server cert (SNI=$SNI)"
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out server.key
openssl req -new -key server.key -subj "/CN=$SNI" -out server.csr
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days "$DAYS" \
  -extfile <(printf "subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\n" "$SNI") -out server.crt

echo "==> client cert (CN=$CLIENT_CN)"
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out client.key
openssl req -new -key client.key -subj "/CN=$CLIENT_CN" -out client.csr
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days "$DAYS" \
  -extfile <(printf "extendedKeyUsage=clientAuth\n") -out client.crt

rm -f server.csr client.csr
echo "==> done: $OUT_DIR"
ls -1 "$OUT_DIR"
