# Running the honey web UI in Docker

The image's default command starts the token-protected web UI:

```
ENTRYPOINT ["/usr/local/bin/honey"]
CMD ["web", "--listen", "0.0.0.0:8765", "--browser=false", "--debug-log=/dev/stderr"]
```

Because the container can't open a browser for you, you must supply the auth token
yourself. There are three supported ways, in order of preference.

## 1. Set a fixed token (recommended)

Pass `HONEY_WEB_TOKEN`. The same token is then valid on every start, and you can
build the UI URL up front:

```sh
docker run -d --name honey \
  -p 8765:8765 \
  -e HONEY_WEB_TOKEN=please-change-me \
  -v honey-data:/data \
  ghcr.io/shareed2k/honey:latest

# open: http://localhost:8765/?token=please-change-me
```

After the first visit to the `?token=…` URL the browser stores a `honey_proxy_token`
cookie, so the bare `http://localhost:8765/` works for the rest of the session.

## 2. Let honey generate and persist a token

If `HONEY_WEB_TOKEN` is not set, honey generates a random token and persists it to
`$XDG_STATE_HOME/honey/web_token` — which is `/data/state/honey/web_token` in the
image. **Mount `/data` as a volume** so the token survives restarts:

```sh
docker run -d --name honey -p 8765:8765 -v honey-data:/data ghcr.io/shareed2k/honey:latest
docker logs honey      # prints: URL: http://0.0.0.0:8765/?token=<token>
```

Open the printed `?token=…` URL once; the token stays stable across `docker restart`.
(Without a mounted `/data`, a new token is generated on every start.)

## 3. Disable auth (trusted networks / authenticating proxy only)

Use `--no-auth` or `HONEY_WEB_NO_AUTH=1` when honey sits behind an authenticating
reverse proxy or runs on a fully trusted network. This turns off the token check for
all API, WebSocket, and proxy routes — do **not** expose such a container publicly.

```sh
docker run -d --name honey -p 8765:8765 -e HONEY_WEB_NO_AUTH=1 \
  ghcr.io/shareed2k/honey:latest
```

## Behind a reverse proxy (nginx / traefik / caddy)

- Forward the `Authorization` and `X-Honey-Token` request headers to the container.
- Don't strip the `honey_proxy_token` cookie.
- Set `X-Forwarded-Proto: https` (or terminate TLS) so honey marks the cookie
  `Secure` when the browser is on HTTPS; over plain HTTP the cookie is non-`Secure`
  so it still works.

## Configuration

`HONEY_CONFIG=/data/config.yaml` is preset. Mount your own config there:

```sh
-v /path/to/config.yaml:/data/config.yaml
```
