---
title: Add a New Backend
---

# Add a New Backend

Use this guide when introducing a new provider/backend in honey.

## 1) Add config model

In `internal/config/config.go`:

- define `type NewBackend struct { ... }`
- add `Backends.NewBackend []NewBackend` with matching `yaml`/`json` tags

Use snake_case tags for YAML compatibility and add `honey` tags for schema metadata.

Typical tag usage:

- on backend list field in `Backends`:
  - `honey:"label=My Backend;order=60"`
- on each backend field:
  - `honey:"label=Token;secret"`
  - `honey:"label=Mode;enum=nodes|pods;enum_as_warning;default=nodes"`

## 2) Register provider and backend slice once

In `internal/provider/<newbackend>/factory.go`, keep `searchrun.Register(...)` and implement:

```go
func (newbackendFactory) BackendKind() string { return "newbackend" }
func (newbackendFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.NewBackend }
```

This is the single source of truth for backend-kind registration.  
Web CRUD (`/api/v1/config/backends/...`) uses this central registry via `searchrun`.

Also ensure package import is present in `internal/provider/all/all.go` so `init()` runs.

## 3) Wire search/provider behavior

Add provider implementation in `internal/provider/<newbackend>` and wire flags/defaults in `searchrun` paths as needed so:

- `honey search --provider <newbackend>` works
- `/api/v1/search` resolves this backend

## 4) Schema is generated from tags

You usually do not hardcode backend field maps in `internal/config/schema.go`.

The schema builders reflect over `config.go` tags and produce:

- `ui_schema` (dynamic forms + YAML lint)
- `json_schema`

So adding backend fields is primarily a tag/config-model change.

Schema endpoint remains: `GET /api/v1/config/schema`.

## 5) UI behavior

No manual form/lint wiring is usually needed if schema is complete:

- `webui/src/ConfigBackendsSection.tsx` renders backend forms dynamically from `ui_schema`
- `webui/src/RawYamlEditor.tsx` lints from schema
- Save YAML is blocked when diagnostics exist (warnings + errors)

## 6) Validation checklist

```bash
go test ./internal/searchrun ./internal/webserver ./internal/provider/...
go test ./internal/config
cd webui && npm run build
```

Manual smoke checks:

- search works for new provider
- config tab Add/Edit works for new backend
- YAML lint catches invalid schema fields
- Save YAML blocks while warnings/errors exist
- `/api/v1/config/schema` contains your backend under `ui_schema.backends` and `json_schema.properties.backends.properties`

