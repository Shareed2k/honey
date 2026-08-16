---
id: sso-login
title: SSO Login (OIDC)
slug: /sso-login
---

`honey kube login` and `honey ssh login` can authenticate you with your
corporate single sign-on identity instead of an operator-issued enrollment
code: you sign in through a browser, honey verifies the resulting OIDC
`id_token`, an OPA policy maps your verified claims to a honey identity, and
honey issues a short-lived certificate — an mTLS client certificate for the
[Kubernetes access proxy](./k8s-proxy.md), an SSH certificate for the
[SSH gateway](./ssh-gateway.md) — that the gateways already know how to
consume. No honey-specific credential is created or stored anywhere; your SSO
session is the only secret involved, and it never leaves your machine.

This is opt-in: with no `oidc:` block configured, the login endpoints don't
exist (`404`) and nothing about existing enrollment changes.

## The two-policy model

Authorization here is split across two independent OPA decisions, each with
its own `input.action`:

- **`identity`** — the *role* decision. It runs once, at login, and maps your
  verified SSO claims (email, groups, the raw token) to a Kubernetes user +
  groups and/or a set of SSH principals. This is the only place group/role
  mapping happens; the gateways never see your SSO claims.
- **`k8s_request`** (documented in full in [Kubernetes Access
  Proxy](./k8s-proxy.md#policy--audit)) — the *resource* decision. It runs on
  every proxied Kubernetes API call and authorizes it by the identity baked
  into your certificate (never your original SSO claims, which the gateway
  never sees again after login).

The SSH gateway has no per-request resource policy of its own beyond
certificate principal matching — see [SSH Gateway](./ssh-gateway.md) for how
principals map to inventory access there.

Both policies live in the same `.rego` package (`HONEY_POLICY_DIR`) as every
other honey decision — see [Authorization](./authorization.md).

## Server configuration

Enable SSO login with an `oidc:` block:

```yaml
# config.yaml
oidc:
  issuer: https://your-oidc-provider.example/realms/corp  # OIDC discovery issuer
  client_id: honey-kube                                    # expected token audience
  scopes: ["groups"]           # additional scopes; openid/email/groups are always requested
  username_claim: email        # claim mapped into the identity input as `email` (default: email)
  groups_claim: groups         # claim mapped into the identity input as `groups` (default: groups)

device_cert_ttl: 12h           # validity of every device/SSO-issued certificate (default: 12h)

k8s_proxy:
  clusters:
    - name: prod
      kubeconfig: /etc/honey/prod.kubeconfig
      labels:                  # exposed to k8s_request policy as input.cluster_labels
        env: prod
        region: us-east
```

`issuer` and `client_id` are the only required fields — omit `username_claim`
/ `groups_claim` to use the common `email` / `groups` claim names your
provider likely already issues. `scopes` supplements, it doesn't replace,
`openid email groups`.

`k8s_proxy.clusters[].labels` are arbitrary key/value tags for a cluster,
independent of OIDC — they exist so a `k8s_request` policy can select by
attribute (environment, region, platform) instead of only by cluster name.
See the [full `k8s_proxy` config reference](./k8s-proxy.md#configuration-reference).

### `device_cert_ttl` and why it's short

honey has **no certificate revocation**: once issued, a certificate is valid
for its full lifetime no matter what happens to the identity behind it
(offboarding, a revoked SSO session, a compromised laptop). The mitigation is
keeping that lifetime short. `device_cert_ttl` defaults to **12h** and governs
every certificate honey mints for a human — SSO-issued Kubernetes client
certs, SSO-issued SSH certs, and enrollment-code certs alike. Lower it if your
threat model calls for it; there is no floor enforced, only sane defaults.

## The `identity` policy

At login, honey evaluates:

```json
{
  "action": "identity",
  "target": "kube",          // or "ssh"
  "cluster": "prod",         // requested cluster name; "" for ssh
  "subject": "user-abc123", // the token's sub claim
  "email": "alice@corp.example",
  "groups": ["eng", "on-call"],
  "claims": { "...": "the full decoded id_token claim set" }
}
```

and expects an `identity` object back:

```rego
package honey
import rego.v1

default allow := false

# Map the "eng" SSO group to a honey identity: a Kubernetes user + group, and
# an SSH principal set. `input.email` becomes the certificate CN either way.
identity := {
	"user":       input.email,
	"groups":     ["developers"],
	"principals": ["ubuntu", input.email],
} if {
	input.action == "identity"
	"eng" in input.groups
}

allow if {
	input.action == "identity"
	identity
}
```

This is **fail-closed**: a subject with no `identity` object, or where `allow`
is false, is denied login outright (`403`) — no certificate is issued. There
is no default identity mapping; without a policy that sets `identity`, SSO
login always fails.

`identity.user` becomes the certificate's `CN` (the Kubernetes impersonated
user / the SSH certificate `KeyId`); `identity.groups` becomes the client
certificate's `O=` (Subject Organization) fields, which is what
`k8s_request` policies see as `input.groups`; `identity.principals` becomes
the SSH certificate's valid principals. **Groups and principals are
honey-CA-attested**: they are read back out of the certificate honey itself
signed, never asserted by the client at connection time.

The `identity` policy also receives `input.cluster` (the cluster named at
`honey kube login <cluster>`), but the issued certificate is **not**
cluster-scoped — it carries only the user and groups. Use `input.cluster` to
decide *which identity* to grant, not to fence access to a single cluster;
per-cluster and per-resource access is enforced on every request by the
`k8s_request` policy below (via `input.cluster`, `input.cluster_labels`, and the
request fields), which is the authoritative gate.

## The `k8s_request` policy: fine-grained resource authorization

Once a Kubernetes identity is issued, every proxied API call is a separate
`k8s_request` decision (see [Kubernetes Access
Proxy](./k8s-proxy.md#policy--audit) for the full input shape). Combining
group, cluster label, namespace, resource, verb, and a name regex gives you a
fine-grained, resource-level access role — for example, read-only cluster-wide
access plus a narrower `exec` grant into a specific namespace's dev pods:

```rego
package honey
import rego.v1

default allow := false
default deny_reason := "denied by policy"

# developers: read-only on every "staging"-labelled cluster
allow if {
	input.action == "k8s_request"
	"developers" in input.groups
	input.cluster_labels.env == "staging"
	input.verb in {"get", "list", "watch"}
}

# developers: exec only into pods named "dev-*" in the "sandbox" namespace,
# on the same staging clusters
allow if {
	input.action == "k8s_request"
	"developers" in input.groups
	input.cluster_labels.env == "staging"
	input.namespace == "sandbox"
	input.resource == "pods"
	input.subresource == "exec"
	regex.match(`^dev-`, input.name)
}
```

Because `input.groups` here comes from the certificate's `O=` fields (set by
the `identity` policy at login, not by the caller), a `k8s_request` policy
never needs to trust anything the client sends on the wire — the same
enforcement holds whether the identity came from SSO or an enrollment code.

## User commands

### `honey kube login`

```bash
# SSO branch (default): omit --enroll-code
honey kube login prod --proxy honey-host:6443

# enrollment-code branch: unchanged, still available for CI/headless use
honey kube login prod --enroll-code abc123 \
  --proxy honey-host:6443 --proxy-ca serving-ca.pem
```

Without `--enroll-code`, `honey kube login <cluster>` opens your browser for
an OIDC sign-in, exchanges the verified identity for a signed client
certificate, and writes a `honey-<cluster>` kubeconfig context — same as the
enrollment-code path, but the server also returns its own serving CA in the
response so `--proxy-ca` / `--insecure-skip-tls-verify` are usually
unnecessary (they remain available as an override, and as mutually-exclusive
alternatives if the server doesn't know its own CA). `--admin-url` selects the
honey web instance the browser flow talks to (default `$HONEY_WEB_URL`, else
`http://localhost:8765`); `--proxy` is always required — it's the address
kubectl connects to, independent of where the login itself happens.

```bash
kubectl --context honey-prod get pods
```

### Auto-refreshing kube login (kubectl exec credential plugin)

The SSO branch of `honey kube login <cluster>` writes a kubeconfig `user`
entry that is a **kubectl exec credential plugin**, not an embedded
certificate: instead of storing `client-certificate-data` /
`client-key-data`, it stores a command (`honey kube login <cluster>
--admin-url <url>`) that kubectl runs on demand. kubectl calls this command
before every request that needs credentials, so `honey` transparently
re-authenticates through your OIDC provider whenever the cached certificate
has actually expired — the browser opens **only** on that expiry path, never
on every call, and `kubectl` never surfaces an expired-certificate error. You
never manually re-run `honey kube login` because it expired; `kubectl` does
it for you the next time you use the context. During interactive sign-in
(setup or a triggered refresh) the browser now opens automatically; the
sign-in URL is still printed to stderr as a fallback for when a browser
can't be opened.

```bash
honey kube login prod --proxy honey-host:6443
kubectl --context honey-prod get pods   # works today...

# ...and still works after the certificate expires: kubectl silently re-runs
# "honey kube login prod --admin-url ..." behind the scenes, honey notices
# the cached cert is stale, opens the browser for a fresh sign-in, and
# hands kubectl a new certificate — no re-login command, no error.
kubectl --context honey-prod get pods
```

**Prerequisite: `honey` must be on the `PATH` kubectl uses.** kubectl
launches the exec plugin as a subprocess by the bare command name (`honey`),
resolved through its own environment's `PATH` — not necessarily an
interactive shell's. If `honey` isn't reachable there, every request fails
with a generic exec-plugin error. Confirm it with `which honey` in the same
environment kubectl runs in, or use `--static` below instead.

**`--static` opts out.** `honey kube login prod --proxy ... --static` embeds
the certificate and key directly in the kubeconfig, reproducing the
pre-auto-refresh behavior: no exec plugin, no re-authentication, and no
`honey` PATH requirement at kubectl-call time. Use it on hosts where `honey`
won't be installed alongside kubectl (e.g. a kubeconfig handed to a CI
runner or another machine). Because there's no cached SSO session to refresh
from, you re-run `honey kube login` by hand once the certificate expires.
The `--enroll-code` path is always static, regardless of `--static` — an
enrollment code has no SSO session to refresh from either.

**`--no-browser`** prints the sign-in URL to stderr instead of attempting to
open a browser, for headless hosts and SSH sessions — the same flag works
whether the sign-in is the initial `honey kube login` or a refresh triggered
by the exec plugin.

**Credential cache.** A successful sign-in caches the issued certificate and
key under `~/.honey/kube/<cluster>/` (honors `$HONEY_HOME`), with the files
written `0600`. This cache is what lets the exec plugin serve most requests
with no network round trip at all — it only re-authenticates when the cached
certificate is within a minute of `NotAfter`. The exec-plugin kubeconfig
entry itself carries no certificate or key material — just the command used
to fetch them — so a leaked kubeconfig alone can't be used as a credential.

**Non-interactive kubectl (CI, `interactiveMode: Never`).** Some kubectl
invocations tell exec plugins they may not prompt or open a browser. If the
cached certificate has expired in that context, `honey` fails closed with an
error instead of hanging on a browser that will never be seen — refresh the
cache first by running `honey kube login <cluster>` interactively (with a
real browser or `--no-browser`) from an environment that allows it.

**Composes with brokered intercept.** Brokered `honey intercept --cluster
<cluster>` (see [Local Interception](./intercept.md)) resolves the same
`honey-<cluster>` kubeconfig context that `honey kube login` writes, so its
port-forward inherits this auto-refresh for free — no separate credential
path to keep alive for intercept.

### `honey ssh login`

```bash
honey ssh login --admin-url https://honey.example
```

Runs the same browser OIDC sign-in, then exchanges the identity for a signed
SSH user certificate. `--identity` selects the private key to certify
(default `~/.ssh/honey_ed25519`; generated on first use if it doesn't exist);
`--out` overrides where the certificate is written (default:
`<identity>-cert.pub`, which OpenSSH loads automatically alongside the key).

```bash
ssh -i ~/.ssh/honey_ed25519 alice@gateway-host -p 12222 <resource>
```

## Security model

- **PKCE (S256), `state`, and a loopback-only redirect.** The CLI generates a
  fresh PKCE verifier/challenge and a random `state` per login, listens on
  `127.0.0.1:0`, and only accepts the authorization code back on that
  loopback address — there is no shared client secret and no
  network-reachable redirect target.
- **A caller-bound `nonce` (defense-in-depth, not a replay store).** The CLI
  generates a random `nonce`, sends it with the authorization request, and honey
  re-verifies it against the `id_token`'s `nonce` claim — binding the token to
  the caller's own login request. honey stores no nonce server-side, so this is
  **not** a replay guard for a leaked, still-valid token; short token lifetimes
  (the provider controls the `id_token` TTL) and TLS transport are the
  mitigations there. The `id_token` is a bearer credential — treat it as a
  short-lived secret.
- **Full `id_token` verification, fail-closed.** honey verifies the token's
  signature against the issuer's published keys, and checks `iss`, `aud`
  (must equal `client_id`), and `exp` — an `alg: none` token, an expired token,
  a wrong-audience token, or a nonce mismatch are all rejected outright. Any
  verification or policy-evaluation error denies the login; nothing defaults to
  allow.
- **Groups and principals are honey-CA-attested, never client-asserted.**
  The `identity` policy's output is baked into the certificate at issuance
  (`O=` for Kubernetes groups, valid principals for SSH); the gateways read
  those fields back out of the certificate they verified, so a client cannot
  claim a different group or principal after login by altering a request.
- **No secrets logged.** The `id_token`, certificates, CSRs, and public keys
  are never written to the audit log or application logs — only the action,
  target, resolved actor, and allow/deny decision (`kube_login` / `ssh_login`
  audit events).
- **Short-lived certificates, no revocation.** See
  [`device_cert_ttl`](#device_cert_ttl-and-why-its-short) above — this is the
  primary mitigation for a compromised SSO session or an offboarded user
  whose certificate is still technically valid.
