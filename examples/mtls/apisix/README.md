# mTLS to honey via Apache APISIX

Front honey with [Apache APISIX](https://apisix.apache.org/) so clients (e.g. the
android app) authenticate with a **per-device client certificate** (mTLS) instead
of a shared token. APISIX verifies the client cert, extracts its identity, and
forwards it to honey as the trusted-proxy header `X-Honey-User`, which becomes the
OPA policy actor.

```
client (cert CN=honey-app)
   │  mTLS  ─ APISIX verifies cert against client.ca
   ▼
APISIX :9443 ── serverless-pre-function: X-Honey-User = CN ──► honey :8765 (HTTP, private)
                proxy-rewrite: X-Ssl-Client-Fingerprint          HONEY_TRUSTED_PROXIES trusts APISIX
                                                                 actor = honey-app → OPA policy
```

## How identity flows

- **SSL resource** ([apisix.yaml](apisix.yaml)) sets `client.ca` + `client.depth`, so APISIX
  requires and verifies a client cert on the `honey.example` SNI.
- APISIX exposes the verified cert via NGINX vars (`$ssl_client_s_dn`,
  `$ssl_client_fingerprint`, …). A `serverless-pre-function` strips the CN out of
  the subject DN (`CN=honey-app` → `honey-app`) and sets `X-Honey-User`.
  (Prefer the clean CN; alternatively drop the function and forward
  `X-Honey-User: $ssl_client_s_dn` via `proxy-rewrite`, then key the policy on the
  full DN.)
- honey trusts `X-Honey-User` only from peers in `HONEY_TRUSTED_PROXIES`, and uses
  it as the OPA actor — pair with [../../policy/app-backends](../../policy/app-backends).

## Run the demo

```bash
# 1. demo PKI (CA + server cert for honey.example + client cert CN=honey-app)
./gen-certs.sh                 # writes ./certs (gitignored)

# 2. inline the PEMs into the APISIX standalone config
./render.sh                    # writes ./apisix.local.yaml (gitignored)

# 3. honey on the host, reachable ONLY via APISIX, trusting the gateway:
HONEY_TRUSTED_PROXIES=0.0.0.0/0 \
HONEY_POLICY_DIR=../../policy/app-backends \
honey web --no-auth --listen localhost:8765
#   NOTE: --no-auth is safe ONLY because honey is bound to localhost and reached
#   solely through APISIX. HONEY_TRUSTED_PROXIES here is wide for the local demo;
#   in production set it to the APISIX node/pod CIDR.

# 4. APISIX
docker compose up -d
```

## Verify

```bash
# valid client cert -> 200, actor "honey-app", policy allows backends
curl --resolve honey.example:9443:127.0.0.1 --cacert certs/ca.crt \
     --cert certs/client.crt --key certs/client.key \
     https://honey.example:9443/api/v1/backends

# same identity, non-allowed endpoint -> 403 (policy)
curl --resolve honey.example:9443:127.0.0.1 --cacert certs/ca.crt \
     --cert certs/client.crt --key certs/client.key \
     https://honey.example:9443/api/v1/recordings

# no client cert -> APISIX refuses the handshake (400/495)
curl --resolve honey.example:9443:127.0.0.1 --cacert certs/ca.crt \
     https://honey.example:9443/api/v1/backends
```

## Production notes

- Replace the demo CA with **honey's device CA** (Phase 2 enrollment): put that CA
  in the SSL resource's `client.ca`, and per-device certs (CN = `device:<id>`) get
  a distinct actor for per-device policy + revocation.
- Bind honey to a private interface; set `HONEY_TRUSTED_PROXIES` to the APISIX
  network only. Keep TLS termination and client-cert verification at APISIX.
- Kubernetes: express the same with `ApisixTls` (client CA) + `ApisixRoute`
  (proxy-rewrite / serverless-pre-function) CRDs via the APISIX Ingress Controller.
- APISIX admin-API equivalent of [apisix.yaml](apisix.yaml): `PUT /apisix/admin/ssls/1`
  with `client.ca`/`client.depth`, and `PUT /apisix/admin/routes/1` with the same
  `plugins` + `upstream`.
