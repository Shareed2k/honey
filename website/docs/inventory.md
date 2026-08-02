---
id: inventory
title: Dynamic Inventory Variables
---

Honey allows you to enrich discovered hosts with inventory variables. These variables are applied **after** provider discovery, allowing you to attach configuration data to dynamic cloud instances (AWS, GCP, Kubernetes) based on their metadata.

Inventory variables are scalar values (strings, numbers, booleans) that can be passed as `HONEY_VAR_*` environment variables to commands and evaluated in CUE recipes.

## Configuration Structure

Inventory variables are defined in the `inventory` section of your `honey.yaml`. Precedence runs from lowest to highest: **global vars** → **group vars** (ordered by `priority`) → **host vars**.

```yaml
inventory:
  # Global variables applied to all discovered hosts
  vars:
    timezone: UTC
    log_level: info

  groups:
    # Groups apply variables when the CEL match expression returns true
    prod-web:
      priority: 100
      match: "host.provider == 'aws' && host.meta['env'] == 'prod'"
      vars:
        deploy_env: prod
        service: nginx
        allow_restart: true

    # You can use CEL regex matching for dynamic evaluation
    canary:
      priority: 200
      match: "host.name.matches('^web-canary-[0-9]+$')"
      vars:
        service: nginx-canary

  hosts:
    # Exact host overrides. Keys must match either the host name
    # or the full identity string: provider/name/primary_ip
    web-canary-01:
      vars:
        restart_timeout: 10
    aws/web-02/10.0.1.10:
      vars:
        log_level: debug
```

### Match Expressions and Regex

The `match` field is a [CEL expression](https://github.com/google/cel-spec) evaluated against the host record. The `host` variable exposes:
- `host.provider`
- `host.name`
- `host.ip`
- `host.zone`
- `host.region`
- `host.meta['key']`
- `host.extra_ips`

You can use the `.matches()` function for regex evaluation. Note that CEL uses Go's RE2 regex engine.

```yaml
match: "host.name.matches('^web-.*')"
match: "host.zone.matches('^us-east-1[a-z]$')"
match: "host.meta['role'].matches('web|api')"
```

## CLI Search Filters

You can filter discovered hosts by their resolved inventory groups and variables using `--filter` or positional arguments:

```bash
# Using the --filter flag
honey search --filter group:canary
honey search --filter var:service=nginx

# Positional tokens are automatically parsed as filters if they start with group: or var:
honey search group:prod-web var:deploy_env=prod
```

These filters can be combined with native provider search flags:
```bash
honey search --name-regex '^web-' --filter group:prod-web
```

## CUE Recipes

Inventory variables and groups are directly accessible inside CUE recipe `when` conditionals.

```cue
{
  id: "restart_web"
  host: "*"
  when: "in_group('prod-web') && vars.allow_restart == true"
  command: "systemctl restart \"$HONEY_VAR_SERVICE\""
}
```

During step execution, all inventory variables attached to the target host are exported to the remote shell environment with the prefix `HONEY_VAR_`.

:::warning
Inventory variables are visible in plans, JSON outputs, and logs. They are meant for configuration data. Do not use inventory variables for passwords or tokens; use the `secrets` fields for sensitive values instead.
:::

## Ansible dynamic inventory

`honey inventory` exports the same resolved groups/vars as an Ansible-compatible dynamic inventory — built directly on this feature, so `inventory.groups`/`inventory.vars` above double as your Ansible group/host variable source.

```bash
honey inventory --list
honey inventory --host web-canary-01
```

Each host gets `ansible_host` (from the discovered `PrimaryIP`), `ansible_user` (from `--ssh-user` / `defaults.ssh_user`), `honey_*` groups (`honey_provider_*`, `honey_region_*`, `honey_zone_*`), and `honey_meta_*` keys from record meta. Use `--strip-prefix` to drop the `honey_` prefix from group/variable names, and `--blacklist` to exclude specific tags/label keys.

For Ansible's `-i` (which expects a file/directory, not a shell command), either wrap `honey inventory "$@"` in a small executable script, or use the YAML inventory plugin at [`contrib/ansible/inventory_plugins/honey.py`](https://github.com/shareed2k/honey/blob/main/contrib/ansible/inventory_plugins/honey.py) (see `contrib/ansible/honey.gcp.example.yml` and `examples/ansible/README.md`). CLI reference: [`honey inventory`](./cli/honey_inventory.md).
