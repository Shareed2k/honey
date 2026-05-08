---
id: web-ui
title: Web UI & AI assist
---

Honey includes an embedded web server that serves a React-based UI, providing a visual interface for search, configuration, browser terminal sessions, optional session recording, file transfer, and AI-assisted help for terminals and CUE recipes.

The Web UI binds to **loopback only** (`127.0.0.1`, `localhost`, or `::1`) and uses **token-based** authentication.

## Starting the Web UI

```bash
# One-time: embed UI assets into the binary tree (CI usually does this)
make webui

go build -o honey ./cmd/honey
honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
```

Optional flags:

| Flag | Purpose |
|------|---------|
| `--listen` | Host:port (must be loopback; default `127.0.0.1:8765`) |
| `--config` | Honey YAML path (same resolution as `honey search`) |
| `--record-dir` | Directory to store **SSH/K8s terminal session recordings** (enables replay in the UI when set) |
| `--files-root` | Local filesystem root for the file browser (default: `$HONEY_FILES_ROOT` or `$HOME`) |
| `--agent-bin` | Explicit path to the `honey-transfer-agent` binary (optional) |
| `--agent-build-cache-dir` | Cache directory when the server auto-builds the transfer agent |

On startup, Honey prints the URL and auth hints on stderr:

```text
Honey Web UI (Ctrl+C to stop)
  URL:   http://127.0.0.1:8765/?token=...
  API:   Authorization: Bearer <token>  or  X-Honey-Token: <token>
  WS:    /ws/ssh?token=<token>
  Assist: OPENAI_API_KEY (+ optional OPENAI_BASE_URL)
```

Open the **URL** in your browser (the query string includes the token).

### Authentication

- **Default:** a random hex token is generated each run.
- **Fixed token:** set `HONEY_WEB_TOKEN` in the environment before starting `honey web`.
- Pass the token as `?token=…`, or header `Authorization: Bearer <token>`, or `X-Honey-Token: <token>`.

## Features

### Backends, search, and config

- **Providers & backends:** Filters match the CLI; structured CRUD for `backends.*` mirrors the YAML file.
- **Search:** Same discovery as `honey search`, with results in the table.
- **Config:** Raw YAML editor plus schema-backed backend forms and `GET /api/v1/config/schema` for validation.

### Browser terminal

- WebSocket **`GET /ws/ssh?token=…`**: interactive session to **SSH hosts** (system `ssh` behavior) and **Kubernetes pods** (ephemeral exec TTY), aligned with the TUI.
- Optional **session recording** when `--record-dir` is set; recordings can be listed and replayed from the UI.

### Files and transfer

- Browse **local** paths under `--files-root` and **remote** paths on connected hosts.
- **Agent-based transfer** (`honey-transfer-agent`): copies between local, remote, and cloud storage using the separate agent binary (paths via `--agent-bin` / build cache).

### CUE recipes

- Open and run recipes from the UI (same semantics as `honey cue-exec`).
- **Recipe assist** (AI): explains or debugs a recipe using the file content plus optional dry-run output for the current host selection (requires `OPENAI_API_KEY`).

## AI assist

Assist features call an **OpenAI-compatible** HTTP API using the official key and optional base URL.

### Setup

| Variable | Required | Description |
|----------|----------|-------------|
| `OPENAI_API_KEY` | **Yes** for assist | API key (or token) for the provider. |
| `OPENAI_BASE_URL` | No | Override API base URL (e.g. local [LM Studio](https://lmstudio.ai/), Azure OpenAI-compatible gateway). If unset, the default OpenAI endpoint is used. |

After setting the key, restart `honey web`. **`GET /api/v1/meta`** includes `"terminal_assist_available": true` when Assist is configured.

**Models:** the UI loads chat models from **`GET /api/v1/terminal-assist/models`** (provider `ListModels`, cached briefly). You must pick a model **returned by that endpoint** for completions.

### What Assist does

1. **Terminal assist** — `POST /api/v1/terminal-assist`  
   Sends your question plus **terminal scrollback** (tail) to the model with a short system prompt aimed at shell/errors/next steps. Useful when you are stuck in an SSH or pod session.

2. **Recipe assist** — `POST /api/v1/recipes/assist`  
   Sends the recipe CUE source, parse/validation notes, and **dry-run plan text** (when hosts are selected) so the model can explain steps, risks, and fixes. It does not execute commands.

### Limits and tuning (environment)

| Variable | Default | Meaning |
|----------|---------|---------|
| `TERMINAL_ASSIST_MAX_SCROLLBACK_RUNES` | `24000` | Max Unicode runes of scrollback tail sent upstream. |
| `TERMINAL_ASSIST_MAX_USER_RUNES` | `4000` | Max runes for the user question. |
| `TERMINAL_ASSIST_RPM` | `30` | Max Assist requests **per client IP per minute** (sliding window). |
| `TERMINAL_ASSIST_MAX_TOKENS` | `1024` | Max completion tokens. |
| `TERMINAL_ASSIST_UPSTREAM_SEC` | `90` | Upstream request timeout (seconds). |

### Privacy and security

- Assist sends **scrollback and prompts to the configured API**. Do not paste secrets; rotate credentials if they may have leaked into a session.
- Loopback binding and bearer token reduce exposure but **do not encrypt** traffic to third-party AI providers—use only keys and endpoints you trust.

## API reference

Authenticate all routes below except static files: `Authorization: Bearer <token>` or `X-Honey-Token: <token>` (or `?token=` for GET).

### Meta and discovery

- `GET /api/v1/meta` — version, config path, `session_recording_available`, `terminal_assist_available`.
- `GET /api/v1/providers` — search provider IDs (e.g. `k8s`).
- `GET /api/v1/backends` — configured backends.

### Search and exec

- `POST /api/v1/search` — search hosts (JSON body mirrors CLI search input).
- `POST /api/v1/exec` — remote execution from the UI.
- `POST /api/v1/cue-exec` — run CUE recipes.

### Config

- `GET` / `PUT /api/v1/config` — raw YAML.
- `GET /api/v1/config/schema` — schema for forms and lint.
- `GET /api/v1/config/backends`, `POST /api/v1/config/backends/{kind}`, `PUT`/`DELETE` with index — structured backend CRUD.

### Files and uploads

- `POST /api/v1/upload` — SFTP-style upload (drag-and-drop in UI).
- `POST /api/v1/files/local/list` — list under local root.
- `POST /api/v1/files/remote/list` — list on remote host.
- `POST /api/v1/files/copy` — copy between locations.
- `POST /api/v1/files/agent-transfer` — cloud/agent pipeline.

### Recipes

- `GET /api/v1/recipes` — list known recipe files (server allowlist).
- `POST /api/v1/recipes/view` — read/validate recipe content.
- `POST /api/v1/recipes/assist` — **recipe assist** (requires `OPENAI_API_KEY`).

### Recordings

- `GET /api/v1/recordings` — list recordings when `--record-dir` is set.
- `POST /api/v1/recordings/play` — fetch payload for replay.

### Terminal WebSocket and AI

- `GET /ws/ssh?token=…` — browser terminal (not JSON).
- `GET /api/v1/terminal-assist/models` — model IDs for Assist.
- `POST /api/v1/terminal-assist` — **terminal assist** (requires key; JSON body: `user_prompt`, `scrollback`, optional `max_lines`, `model`).

## Local UI development

The frontend lives in `webui` (Vite + React).

1. Start the Go server:

   ```bash
   honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
   ```

2. In another terminal:

   ```bash
   cd webui
   npm install
   npm run dev
   ```

3. Open the dev URL (typically `http://localhost:5173`). Vite proxies API/WebSocket to the Go backend.

For production, build assets into the embed tree:

```bash
make webui
```

Then rebuild `honey` so `internal/webserver/static` is included.
