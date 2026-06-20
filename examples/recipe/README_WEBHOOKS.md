# Hostctl Webhooks Example

This directory contains an example demonstrating how to trigger `hostctl` recipes remotely using webhooks. Webhooks are perfect for CI/CD integrations, event-driven infrastructure, or external systems calling into hostctl.

## Prerequisites

1. Have `hostctl` compiled and ready.
2. Set up the authorization secret that your webhook expects.

```bash
export MY_WEBHOOK_SECRET="super-secret-token"
```

## Running the Server

Start the hostctl server using the provided `webhook_test_app.yaml` configuration.

```bash
hostctl server --config examples/recipe/webhook_test_app.yaml
```

## Triggering the Webhook

With the server running (defaulting to port 8080), you can trigger the webhook using `curl`. 

The URL format is: `http://<host>:<port>/api/v1/webhooks/<app_name>/<webhook_name>`
In our example:
- **app_name**: `webhook_demo` (defined in `webhook_test_app.yaml`)
- **webhook_name**: `github_push` (defined in `webhook.cue`)

Run the following command in a new terminal:

```bash
curl -X POST http://localhost:8080/api/v1/webhooks/webhook_demo/github_push \
  -H "Authorization: super-secret-token" \
  -H "Content-Type: application/json" \
  -d '{
    "after": "abc123def456", 
    "repository": {"full_name": "shareed2k/hostctl"}, 
    "pusher": {"name": "alice"}
  }'
```

You should see a JSON response with the execution result from the localhost step, containing the echo output extracting fields from the JSON payload.

## Changing to Async

If your recipe takes a long time, you can change `async: true` in `webhook.cue`. When async is enabled, the server will return a `202 Accepted` and a Job ID immediately. You can then query the Job ID against `/api/v1/webhooks/results/<id>` to retrieve the results (Note: this requires configuring `record_dir` on your server to persist async job logs).
