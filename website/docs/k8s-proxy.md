---
id: k8s-proxy
title: Kubernetes Access Proxy
slug: /k8s-proxy
---

`honey web` can run an optional **Kubernetes access proxy**: a second, mTLS-only
listener that fronts one or more real Kubernetes API servers. Users point
`kubectl` at honey instead of the cluster; honey terminates TLS, authenticates
the caller by an honey-issued **mTLS client certificate**, maps that identity
to a Kubernetes user, and forwards the request to the real API server **as
that user via Kubernetes impersonation** — gated by the shared
[OPA policy](./authorization.md) and recorded to the [audit log](#policy--audit).
It is enabled by a `k8s_proxy:` block in the config `honey web` loads; there is
no separate `honey k8s-proxy` daemon or command.

## Quick start

```yaml
# config.yaml
k8s_proxy:
  listen: "0.0.0.0:6443"
  clusters:
    - name: prod                        # kubectl reaches it at /prod
      kubeconfig: /etc/honey/prod.kubeconfig
      impersonate:
        user_from: cn                   # client-cert CN -> Impersonate-User
        default_groups: ["honey-viewers"]
```

```bash
# 1. Start honey web with the block above.
honey web --config config.yaml

# 2. Operator mints a one-time enrollment code; the CN becomes the
#    impersonated Kubernetes user for whoever redeems it.
honey device enroll-code --cn alice --admin-url http://localhost:8765

# 3. User redeems the code: this signs a client certificate and writes a
#    kubectl context that points at the proxy.
honey kube login prod \
  --enroll-code <code> \
  --proxy honey-host:6443 \
  --proxy-ca serving-ca.pem

# 4. Use it like any other context.
kubectl --context honey-prod get pods
```

`--proxy-ca` should point at the proxy's serving certificate (see
[TLS](#tls) below); use `--insecure-skip-tls-verify` instead only for local
testing.

## Cluster-side RBAC prerequisite

Kubernetes impersonation is itself an RBAC-gated action: the identity in the
kubeconfig honey uses to reach a cluster (`clusters[].kubeconfig`) must be
allowed to impersonate the users/groups the proxy sets. Without this the API
server rejects every impersonated call with `403 Forbidden`, no matter what
honey's own policy decides. Grant it with a standard `ClusterRole` /
`ClusterRoleBinding`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: honey-impersonator
rules:
  - apiGroups: [""]
    resources: ["users", "groups"]
    verbs: ["impersonate"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: honey-impersonator-binding
subjects:
  - kind: ServiceAccount     # or User/Group — whatever identity the
    name: honey              # kubeconfig in clusters[].kubeconfig authenticates as
    namespace: honey
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: honey-impersonator
```

If that kubeconfig's identity already has `cluster-admin` (or another
wildcard `*` role), impersonation is implicitly included and no extra
binding is needed.

## Identity mapping

Every request through the proxy carries a verified client certificate (the
listener requires and verifies one via TLS `RequireAndVerifyClientCert`); its
**CommonName is the honey actor**. For each cluster, `impersonate.user_from:
cn` (the only supported value today) sets `Impersonate-User` to that CN, and
each entry in `impersonate.default_groups` is added as an `Impersonate-Group`.

Before setting its own impersonation headers, the proxy **deletes any
`Authorization` and `Impersonate-*` headers the client sent** — a user cannot
smuggle `Impersonate-User: cluster-admin` on their own request. This header
strip is the proxy's core security control.

An unknown cluster (no `/<cluster>` match) or an actor with no identity for
that cluster both return a generic `404` — the boundary never reveals which
clusters exist to an unmapped caller.

## Policy & audit

Every request is evaluated by the same OPA enforcer the rest of `honey web`
uses (`defaults.policy_dir` / `HONEY_POLICY_DIR` — see
[Authorization](./authorization.md)), fail-closed: a policy evaluation error
denies rather than opening the boundary. The action is `k8s_request`:

```rego
package honey
import rego.v1

allow if {
	input.action == "k8s_request"
	input.cluster == "prod"
	input.verb in {"get", "list", "watch"}
}
```

Input fields: `actor`, `cluster`, `verb` (`get`/`list`/`watch`/`create`/
`update`/`patch`/`delete`, derived from the HTTP method and path),
`resource`, `namespace`, `name`, `subresource` (e.g. `exec`, `log`,
`portforward`). No policy configured allows every request (subject to the
cluster's own RBAC).

Every decision — allow or deny — is written to the audit log as one
`k8s_request` event (`source=web`) with the actor, cluster, and parsed
verb/resource/namespace/name/subresource, but never certificate or credential
material:

```bash
honey audit tail --action k8s_request
honey audit export --action k8s_request --format jsonl
```

## TLS

The proxy terminates TLS itself, independent of the rest of `honey web`:

- **Serving certificate** — `tls_cert` / `tls_key` if set; otherwise honey
  generates and persists a self-signed EC (P-256) certificate under the state
  dir (`k8sproxy_serving.crt` / `k8sproxy_serving.key`), valid for one year
  and covering `localhost` / `127.0.0.1` plus any configured host. Point
  `honey kube login --proxy-ca` (or kubectl's `certificate-authority`) at that
  file, or use `--insecure-skip-tls-verify` for local testing.
- **Client authentication** — `client_ca` if set, otherwise honey's built-in
  device CA (the same CA `honey device enroll-code` / `honey kube login` use).
  The listener is configured with `tls.RequireAndVerifyClientCert`: a
  connection without a valid client certificate never reaches the handler.

## Limitations / security model

- **Streaming exec/port-forward is proxied, not recorded.** `kubectl exec`,
  `logs -f`, and `port-forward` connections are forwarded transparently
  (the reverse proxy passes through HTTP upgrades and flushes immediately),
  but today honey only audits the *request* that opened the stream
  (`k8s_request`, with `subresource: exec` / `portforward` / `log`) — the
  stream's content is not captured or recorded, unlike the SSH gateway's
  session recordings. The `record` key in `k8s_proxy:` is reserved for a
  future version.
- **Path-prefix routing.** Each configured cluster is addressed as
  `/<cluster>/...`; there is no cluster discovery or listing endpoint.
- **Deny-by-default.** An unconfigured cluster or unmapped actor returns
  `404`; an OPA deny or cluster-side RBAC denial returns `403`. The listener
  refuses connections without a verified client certificate.
- **The header strip is the key control.** Impersonation headers set by the
  proxy are authoritative only because client-supplied `Impersonate-*` /
  `Authorization` headers are removed first (see
  [Identity mapping](#identity-mapping)) — this, not the OPA policy, is what
  prevents identity forgery.

## Configuration reference

```yaml
k8s_proxy:
  listen: "0.0.0.0:6443"        # host:port for the mTLS listener; unset = proxy disabled
  tls_cert: ""                  # serving certificate path (default: self-signed under the state dir)
  tls_key: ""                   # serving key path (paired with tls_cert)
  client_ca: ""                 # mTLS client CA path (default: honey's built-in device CA)
  policy_dir: ""                # reserved; the proxy currently shares honey web's OPA enforcer
                                 # (defaults.policy_dir / HONEY_POLICY_DIR), not this key
  record: false                 # reserved for future session recording; has no effect yet
  clusters:
    - name: prod                # kubectl path prefix: /prod
      kubeconfig: /etc/honey/prod.kubeconfig   # kubeconfig honey uses to reach the cluster
      context: ""                              # kubeconfig context (default: current-context)
      impersonate:
        user_from: cn           # derive Impersonate-User from the client-cert CN (only supported value)
        default_groups:         # Impersonate-Group values applied to every request on this cluster
          - honey-viewers
```

## For contributors

`internal/k8sproxy` has an end-to-end test that spins up a real k3s cluster
(via testcontainers) and asserts genuine impersonation — that a request
through the proxy is authorized (or denied) by the API server as the
impersonated identity, not honey's own upstream credentials. It's excluded
from normal `go test` / CI runs behind the `k8s_e2e` build tag and requires a
reachable Docker daemon:

```bash
go test -tags k8s_e2e -run TestK8sProxyE2E_Impersonation -v ./internal/k8sproxy/ -timeout 15m
```
