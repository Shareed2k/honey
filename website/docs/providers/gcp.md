---
id: providers-gcp
title: Google Cloud (GCP)
slug: /providers/gcp
sidebar_position: 10
---

# Google Cloud (GCP)

## Overview

Lists **Compute Engine VM instances** in a project (all zones by default, or one zone). Rows use `provider: gcp` with name, primary IP, zone, and region metadata. Execution is via **SSH** to the instance IP.

## Minimal auth

- **Application Default Credentials** on the machine running honey:
  - `gcloud auth application-default login`, or
  - workload identity / GCE metadata when honey runs on GCP.
- **IAM:** permission to list instances, e.g. `compute.instances.list` (`roles/compute.viewer` is sufficient for search).

No API keys in honey YAML; credentials come from the Google auth libraries.

## Config (YAML)

Example file: [examples/config/gcp.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/gcp.yaml)

```yaml
backends:
  gcp:
    - name: my-gcp
      project: my-gcp-project    # required
      zone: us-central1-a        # optional; omit to search all zones
```

Optional per-backend **`docker_discover`** (see [Docker auto-discover](../docker-auto-discover)).

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--gcp-project` | GCP project (or `GOOGLE_CLOUD_PROJECT` / `GCP_PROJECT`) |
| `--gcp-zone` | Single zone filter |

## Verify

```bash
honey search --provider gcp --gcp-project MY_PROJECT -o json
```

## Notes

- Instance must have a usable **external or internal IP** for SSH/connect actions.
- Honey may use `~/.ssh/google_compute_engine` when present for GCE hosts.
- Only instances in `RUNNING` or `STAGING` state are listed.
