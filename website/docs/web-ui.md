---
id: web-ui
title: Web UI
---

Honey includes an embedded web server that serves a React-based UI, providing a convenient visual interface for many of the CLI's core features.

The Web UI is loopback-only by default to ensure it runs safely on your local machine. It uses token-based authentication to secure access.

## Starting the Web UI

To start the Web UI, run the following command:

```bash
honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
```

*Note: You can override the random bearer token by setting the `HONEY_WEB_TOKEN` environment variable.*

Once started, Honey will print the local URL with the access token directly to your terminal:

```text
Honey Web UI (Ctrl+C to stop)
  URL:   http://127.0.0.1:8765/?token=...
```

Open the URL in your browser to start using the Web UI.

## Features

The Web UI provides several features right from your browser:

- **Backends & Providers:** View and manage your configured backend resources. Includes provider and backend filters (dropdowns) and structured CRUD operations that mirror your YAML config.
- **Visual Search:** Perform searches across your providers (e.g. `k8s`, `aws`, `gcp`, `proxmox`) with results displayed dynamically.
- **Config Editor:** Edit your `config.yaml` file directly in the browser.
- **Browser Terminal:** An integrated WebSocket-based terminal (`/ws/ssh?token=...`) allows you to connect directly to SSH nodes and Kubernetes pods via ephemeral exec TTY without leaving your browser.
- **SFTP Upload:** Drag-and-drop file upload support for fast SFTP transfers.

## API Reference

The backend provides several JSON REST API routes that you can interact with. When using the API directly, authenticate via the `Authorization: Bearer <token>` or `X-Honey-Token: <token>` headers.

- `GET /api/v1/meta`: Build and version information.
- `GET /api/v1/providers`: List available provider IDs (e.g., `k8s`).
- `GET /api/v1/backends`: Fetch all configured backends.
- `POST /api/v1/search`: Query search providers.
- `GET`/`PUT /api/v1/config`: Read or update the raw YAML config file.
- `GET`/`POST`/`PUT`/`DELETE /api/v1/config/backends/{provider}/{index}`: Structured CRUD for backends.
- `POST /api/v1/upload`: Trigger SFTP file upload.
- `GET /ws/ssh?token=...`: WebSocket connection for terminal sessions.

## Local Development

If you are developing or modifying the Web UI, the frontend is built using Vite and React (located in the `webui` folder).

1. Start the Go backend on the default port:
   ```bash
   honey web --listen 127.0.0.1:8765
   ```
2. In a separate terminal, navigate to the `webui` directory and start the Vite dev server. The Vite configuration automatically proxies API requests to the Go backend.
   ```bash
   cd webui
   npm install
   npm run dev
   ```
3. Open the Vite local URL shown in your terminal (typically `http://localhost:5173`).

### Building UI Assets

For production builds, the UI assets are bundled into `internal/webserver/static`. This is typically handled by CI automatically, but you can trigger it manually with:

```bash
make webui
```
