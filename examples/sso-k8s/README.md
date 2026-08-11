# SSO Kubernetes access — honey + k3s + an OIDC provider (compose example)

A runnable reference topology showing how a user authenticates to a Kubernetes
cluster with their **SSO identity** through honey:

```
  you ──(browser OIDC login)──▶ identity provider (realm "corp", user alice ∈ group "eng")
   │                                     │  id_token
   │  honey kube login prod              ▼
   └──────────────────────────▶ honey web  ── verifies id_token, runs the OPA
                                  (:8765)     `identity` policy → issues a 1h mTLS cert
                                     │
  kubectl --context honey-prod ─mTLS(cert)─▶ honey k8s proxy (:7443) ── impersonates
                                                 alice + group honey-viewers ──▶ k3s API
```

The identity policy maps IdP group **eng → Kubernetes user `alice@corp` + group
`honey-viewers`**; the RBAC in [k8s/rbac.yaml](k8s/rbac.yaml) lets `honey-viewers`
**read pods** (and the `k8s_request` policy additionally forbids `secrets`).

> This example demonstrates the wiring and is meant to be run and adapted. The
> automated, asserted end-to-end proof of the same chain is `internal/ssoe2e`
> (`go test -tags k8s_e2e -run TestSSOE2E ./internal/ssoe2e/`).

## Files

| Path | Purpose |
| --- | --- |
| [docker-compose.yml](docker-compose.yml) | keycloak + k3s + init + honey |
| [Dockerfile.honey](Dockerfile.honey) | builds honey from the repo (context is the repo root) |
| [keycloak/realm.json](keycloak/realm.json) | realm `corp`, public PKCE client `honey-kube`, group `eng`, user `alice` |
| [honey/config.yaml](honey/config.yaml) | `oidc:` block + `k8s_proxy:` cluster + `device_cert_ttl` |
| [honey/policies/sso.rego](honey/policies/sso.rego) | the `identity` role + the `k8s_request` resource gate |
| [k8s/rbac.yaml](k8s/rbac.yaml) | pods-viewer ClusterRole bound to group `honey-viewers` |

## Prerequisites

- Docker + Docker Compose, and a Docker engine that can run the **privileged
  k3s** container (Linux, Docker Desktop, colima, …).
- A host `kubectl`, and honey built on your host (`go build -o honey ./cmd/honey`
  from the repo root, or an installed `honey`).
- **One hosts entry** so the OIDC issuer URL resolves identically from the honey
  container (Docker DNS) *and* from your host CLI/browser:

  ```
  # /etc/hosts
  127.0.0.1 keycloak
  ```

  This is the usual OIDC-in-compose wrinkle: the `iss` in the token is fixed, so
  every party must reach the provider at the same URL (`http://keycloak:8080`).

## Run

```bash
cd examples/sso-k8s
docker compose up -d --build          # builds honey, starts keycloak + k3s, init wires kubeconfig + RBAC

# wait until honey is up and the proxy is reachable
curl -fsS http://localhost:8765/api/v1/kube/oidc-config    # → {"issuer":"http://keycloak:8080/realms/corp","client_id":"honey-kube",...}
```

Log in with your SSO identity and get a kubeconfig context. The browser opens to
the provider; sign in as **alice / `alice-password`**:

```bash
honey kube login prod \
  --admin-url http://localhost:8765 \
  --proxy localhost:7443 \
  --insecure-skip-tls-verify        # demo only: the proxy serving cert is self-signed for 127.0.0.1/localhost
```

`honey kube login` prints a sign-in URL (open it if it doesn't auto-open),
completes the loopback callback, and writes a `honey-prod` context to your
kubeconfig. Then:

```bash
kubectl --context honey-prod auth whoami     # → alice@corp, groups: [honey-viewers, system:authenticated]
kubectl --context honey-prod get pods -A     # allowed
kubectl --context honey-prod get secrets -A  # Forbidden (denied by honey's k8s_request policy)
```

`honey audit tail` (or the honey web UI) shows one `kube_login` event and a
`k8s_request` event per API call.

## How it fits together

- **Login (once):** the CLI runs a browser Authorization-Code + PKCE flow against
  the provider, sends the `id_token` to `honey web`, which verifies it
  (signature/iss/aud/exp) and runs the `identity` policy. The resolved user +
  groups are baked into a **1h** mTLS client cert (`CN`=user, `O=`=groups).
- **Every request:** `kubectl` presents that cert to honey's proxy over mTLS;
  honey reads the groups from the verified cert, runs the `k8s_request` policy
  (`input.groups`, `input.cluster_labels`, verb/resource/namespace/name), strips
  any client-supplied impersonation headers, sets its own
  `Impersonate-User`/`Impersonate-Group`, and reverse-proxies to k3s. In-cluster
  RBAC then applies to `alice@corp`/`honey-viewers`.

## Teardown

```bash
docker compose down -v
```

## Notes / adapting for production

- **Impersonate RBAC:** this example uses the k3s admin kubeconfig (cluster-admin
  can impersonate). In production, give honey a dedicated service account granted
  a ClusterRole with the `impersonate` verb on users/groups — not cluster-admin.
- **TLS:** drop `--insecure-skip-tls-verify` and pin the proxy serving CA
  (`honey kube login` auto-fills it from the login response when reachable, or
  pass `--proxy-ca`). Front honey web with TLS too.
- **Cert lifetime:** `device_cert_ttl` is the only expiry control (no revocation);
  keep it short.
- **Groups:** the realm attaches a `groups` mapper to the client so the claim is
  in the `id_token`; map your real IdP groups to Kubernetes groups in the
  `identity` policy.
