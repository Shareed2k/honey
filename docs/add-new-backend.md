# Add a New Backend

This guide explains how to add a new backend provider end-to-end in honey.

It covers:

- config model
- search/provider wiring
- web API CRUD
- dynamic UI forms
- schema-driven YAML lint

The current implementation uses config schema as a source of truth for the web UI.

## 1) Add config structs

Define the backend config struct in `internal/config/config.go` and add it to `Backends`.

Example pattern:

- add `type NewBackend struct { ... }`
- add `Backends.NewBackend []NewBackend` with `yaml` and `json` tags

Add `honey` tags so schema/UI/lint are generated automatically.
Recommended metadata:

- backend slice on `Backends`:
  - `label=<Human Name>`
  - `order=<number>`
- fields on `NewBackend`:
  - `label=<Field Label>`
  - optional flags: `secret`, `required`, `enum_as_warning`
  - optional values: `enum=a|b|c`, `default=value`

Keep field names snake_case in tags to match YAML and web API payloads.

## 2) Register provider + backend slice once (single source of truth)

Provider registration now drives backend CRUD lookup dynamically.

In your provider factory (`internal/provider/<newbackend>/factory.go`):

1. keep `searchrun.Register(newbackendFactory{})` in `init()`
2. implement these methods on the same factory:

```go
func (newbackendFactory) BackendKind() string { return "newbackend" }
func (newbackendFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.NewBackend }
```

`searchrun.Register(...)` auto-discovers those methods and populates the central backend registry used by:

- config backends web CRUD (`/api/v1/config/backends/...`)
- dynamic kind validation/error messages

## 3) Add provider/search integration

Wire the new provider in the search path (CLI + web search share this).

Typical places:

- `internal/searchrun` provider registration / flags mapping
- `internal/provider/<newbackend>` implementation
- `internal/hostapi` if provider list/backends list output needs updates

Also ensure the provider package is imported by `internal/provider/all/all.go` so init-time registration runs in CLI/web startup.

Goal: `honey search` and `/api/v1/search` can resolve and query this backend.

## 4) Schema is tag-driven (critical for UI + lint)

You usually do **not** add hardcoded backend fields in `internal/config/schema.go`.
Schema is derived from struct tags in `internal/config/config.go`.

What must be true:

1. New backend slice exists on `Backends` with proper `yaml` + `honey` tags.
2. New backend struct fields have proper `yaml` + `honey` tags.

Then both `BuildUISchema()` and `BuildJSONSchema()` include your backend automatically,
and web UI form/lint pick it up dynamically.

## 5) Verify web schema endpoint

`GET /api/v1/config/schema` (see `internal/webserver/config_handlers.go`) should now include:

- `ui_schema.backends.newbackend`
- `json_schema.properties.backends.properties.newbackend`

Quick check:

```bash
curl -H "X-Honey-Token: <token>" http://127.0.0.1:8765/api/v1/config/schema
```

## 6) UI behavior (usually no manual UI edits needed)

With schema-driven UI in place:

- `webui/src/ConfigBackendsSection.tsx` renders Add/Edit fields from schema
- `webui/src/RawYamlEditor.tsx` validates YAML against schema
- Save is blocked when diagnostics exist (errors and warnings)

Only update frontend code if you introduce a new field type beyond current supported types.

## 7) Update docs and examples

- Add backend section in `README.md` provider/auth table
- Add config YAML example entry under `backends`
- If needed, add CLI flag docs and website docs (`website/docs`)

## 8) Validation checklist

Run:

```bash
go test ./internal/searchrun ./internal/webserver ./internal/provider/...
go test ./internal/config
cd webui && npm run build
```

Manual checks:

- `honey search --provider <newbackend>`
- web config tab -> structured Add/Edit works for new backend
- raw YAML lint highlights invalid new backend fields
- Save YAML blocks on invalid/warning state, allows valid state

Optional runtime check:

```bash
curl -H "X-Honey-Token: <token>" http://127.0.0.1:8765/api/v1/config/schema
```

Confirm your backend appears under:

- `ui_schema.backends.<newbackend>`
- `json_schema.properties.backends.properties.<newbackend>`

