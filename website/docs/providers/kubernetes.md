---
id: providers-kubernetes
title: Kubernetes
slug: /providers/kubernetes
sidebar_position: 30
---

# Kubernetes

## Overview

Lists **cluster nodes** (default) or **pods** from a kubeconfig context. Rows use `provider: k8s`. **Nodes** are SSH targets by IP; **pods** use the Kubernetes API (`kubectl exec` / ephemeral debug containers), not SSH to the pod network.

## Minimal auth

- Valid **kubeconfig** with a context that can reach the API server.
- **Nodes mode:** `get`, `list` on `nodes` (and typically read access to node addresses).
- **Pods mode:** `get`, `list` on `pods`; for debug/exec features, permissions to create **ephemeral containers** on pods honey attaches to.

Use `kubectl auth can-i list nodes` (or `pods`) with the same context to confirm.

## Config (YAML)

Example file: [examples/config/kubernetes.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/kubernetes.yaml)

```yaml
backends:
  kubernetes:
    - name: k8s-staging
      context: staging              # optional
      kubeconfig: ~/.kube/config    # optional; default kubeconfig rules
      mode: nodes                   # nodes | pods (default: nodes)
      debug_image: alpine:3.23      # pods mode — ephemeral debug image
```

Defaults can set `k8s_mode` / `k8s_debug_image` under top-level `defaults:`.

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--kube-context` | Context override |
| `--kubeconfig` | Kubeconfig path |
| `--k8s-mode` | `nodes` or `pods` |
| `--k8s-debug-image` | Debug image for pods mode |

Search filter: `--provider k8s` (not `kubernetes`).

## Verify

```bash
honey search --provider k8s --kube-context staging -o json
honey search --provider k8s --k8s-mode pods -o json
```

## Notes

- Pods mode behavior (ephemeral containers, `tar` over exec) is described on the [documentation home](/) and in the CLI reference.
- Node rows need a reachable **InternalIP** or **ExternalIP** for SSH from honey.
