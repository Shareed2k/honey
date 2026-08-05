# Recipe & plugin performance

How to make `honey cue-exec` runs and docker plugins faster. Most of the heavy
machinery is already pooled and reused per run — the wins below are the levers you
control, ordered by impact.

## 0. Measure first

Don't guess where the time goes — measure, then change one thing.

```bash
honey cue-exec --profile <recipe.cue> "<query>"   # CPU/Mem/Network/SSH stats
honey cue-exec --debug-log /dev/stderr <recipe.cue> "<query>"   # per-step timings
```

`--profile` prints per-run CPU, memory, network and SSH stats. `--debug-log` shows
per-step "tunnel starting/ready", plugin create/ready, and per-host timing. If a
collector is wired, OTEL spans are emitted per step.

## 1. Run independent steps in parallel — `type: "graph"`

The default (linear) mode runs steps **one at a time**, even when they don't depend
on each other. Switch to graph mode and declare dependencies; steps with no
dependency between them run concurrently.

```cue
recipe: {
  name: "example"
  type: "graph"                 // <- opt into parallel steps
  steps: [
    { id: "a", host: "*", command: "..." },
    { id: "b", host: "*", command: "..." },              // a and b run together
    { id: "c", host: "*", depends: ["a", "b"], command: "..." },  // c waits for both
  ]
}
```

Graph mode is a **true dataflow** scheduler: each step starts the instant its own
`depends` finish — a slow step never holds up an unrelated peer, and a step two
levels deep runs as soon as its chain is ready rather than waiting for the slowest
step at its level. By default up to **8 steps** run concurrently; raise it with
`defaults.max_parallel` (see §2), which also sizes the graph worker pool. There is
no per-level barrier, so you get parallelism from declaring dependencies
accurately — over-declaring `depends` serializes work that could overlap.

## 2. Raise host fan-out — `max_parallel`

Within a step, hosts run concurrently. When `max_parallel` is unset, the default
depends on the step kind: **8** for plugin / tunnel / template / k8s steps, **32**
for command / script. For many hosts, raise it:

```cue
recipe: {
  defaults: { max_parallel: 32 }   // applies to every step kind (1-128)
  ...
}
```

Or per step (`max_parallel: N` on the step). To raise it for **all** recipes
without editing each one, set it once in the honey config:

```yaml
# honey.yaml
defaults:
  max_parallel: 32   # seeds the recipe default when a recipe doesn't set one
```

Per-recipe / per-step `max_parallel` always wins over the config default.
`serial: true` on a step forces one-host-at-a-time (only when required).

> Heavier step kinds (remote docker plugins) create a container per host — raising
> concurrency multiplies containers, so size `max_parallel` to what the target and
> the operator can handle.

## 3. Share tunnels and KV sessions

A `tunnel` step **without `share_key` opens one tunnel per host**. Add a `share_key`
so every host/step reuses a single operator-side listener:

```cue
{ id: "pg", host: "db-*", tunnel: { mode: "local", remote_host: "localhost", remote_port: 5432, share_key: "pg" } }
```

Use recipe-scoped `kv_tunnel` (`defaults.kv_tunnel`) rather than per-step, so the
stepkv forward is opened once for the run instead of per host per step.

## 4. Keep secrets cheap in hot loops

Secret refs are resolved **once per run and cached** (per-run memoization), so the
same ref reused across many hosts/steps costs a single backend call. Still, prefer
**local** backends (`secure:`, `age:`, `env:`) for values reused widely — network
backends (`aws-kms:`, `aws-sm:`, `vault:`, `k8s:`) pay a round-trip on the first
resolve. Seal once into a `secure:` ref and reference that, rather than a live KMS
lookup per distinct ref.

## 5. Docker plugins: keep the container warm, avoid per-host multiplication

- **The container is long-lived.** The first plugin call pays create + start +
  readiness; every later call is a cheap HTTP call. Running under `honey web` (a
  persistent process) keeps the container warm across requests; a fresh `honey` CLI
  invocation pays cold start again.
- **Remote plugin runs create one container per host** — cost scales with host
  count. When the plugin just needs to *reach* a service (e.g. pghero → Postgres),
  run it **once on the operator** (`host: "_"`) against a tunneled endpoint instead
  of fanning the container out to every host. See
  [`pghero_tunnel_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/pghero_tunnel_demo.cue)
  and [`pghero_unix_tunnel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/pghero_unix_tunnel.cue).
- Keep `pull_policy: if_not_present` (the default) — `always` re-pulls on every
  container start. Pre-build/pre-pull the image once.
- **Prefer a WASM plugin when the work allows it** — WASM runs in-process (no
  container, no startup), so it beats docker for pure compute/logic. Use docker only
  when you need the container's tools/runtime (ruby / psql / trivy / …).

## 6. Already free (don't reinvent)

Per run, honey already: pools and reuses SSH connections across all steps/hosts;
builds the plugin manager and docker host session once; gathers facts once; and
refcounts tunnels in a shared pool. So the levers above (graph mode, `max_parallel`,
`share_key` / KV scope, secret backend choice, warm / WASM plugins) are where your
tuning pays off.

See also: [CUE recipes](./cue-recipes.md), [Plugin development](./plugins-development.md).
