# Example policy: let an app read backends

[`app_read_backends.rego`](app_read_backends.rego) grants one app identity
read-only access to the backends inventory and host search over the honey REST
API, and denies it everything else. All other actors (operators, the web UI)
are unaffected.

| Actor | Allowed | Denied |
| --- | --- | --- |
| `honey-app` | `GET /api/v1/backends`, `POST /api/v1/search` | every other API request (incl. all writes, e.g. `POST /api/v1/config/backends/{kind}`) |
| anyone else | falls through to the default allow | — |

## Enable

honey's OPA layer is opt-in. Point it at this directory:

```bash
export HONEY_POLICY_DIR=examples/policy/app-backends
```

or in the honey config:

```yaml
defaults:
  policy_dir: examples/policy/app-backends
```

honey evaluates every authenticated REST request as
`{action: "api_request", actor: <id>, method: <verb>, path: <path>}` and reads
`data.honey.allow` (plus `deny_reason`) from the loaded policies.

## App identity

`input.actor` is the caller's JWT `sub` claim (verified with
`HONEY_JWT_PUBLIC_KEY`), or the trusted-proxy user
(`X-Honey-User` when the source IP is in `HONEY_TRUSTED_PROXIES`); otherwise the
legacy shared token maps to `"api"`. So the app must present a JWT signed for
`HONEY_JWT_PUBLIC_KEY` whose `sub` is `honey-app`:

```bash
export HONEY_JWT_PUBLIC_KEY=/path/to/jwt_pub.pem
```

## Customize

- **Rename the actor:** change `app_actor := "honey-app"` to your app's `sub`.
- **Add endpoints:** add another `app_may_read if { ... }` clause matching the
  `input.method` / `input.path` you want to permit.

## Notes

- Keep this in its own directory. The loader reads every top-level `*.rego` in
  `HONEY_POLICY_DIR` into `package honey`; the sibling
  [`../mcp_exec.rego`](../mcp_exec.rego) sets `default allow := false`, which
  conflicts with this file's `default allow := true` if both are loaded together.
- This governs the **REST API** only. Backends listing over the MCP server and
  the `pkg/mobile` binding is not policy-gated.
