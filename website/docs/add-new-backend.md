---
title: Add a New Backend
---

# Add a New Backend

Use this guide when introducing a new provider/backend in honey.

## 1) Add config model

In `internal/config/config.go`:

- define `type NewBackend struct { ... }`
- add a field to the `Backends` struct: `NewBackend []NewBackend` with matching `yaml`/`json` tags, e.g. `` `yaml:"newbackend" json:"newbackend" honey:"label=My Backend;order=70" validate:"dive" mod:"dive"` ``

Use snake_case tags for YAML compatibility and add `honey` tags for schema metadata.

Typical tag usage on each backend field:

- `honey:"label=Token;secret"`
- `honey:"label=Mode;enum=nodes|pods;enum_as_warning;default=nodes"`

## 2) Implement the provider factory

There is no `searchrun.Register(...)` function — a new backend is wired by explicit construction, following the pattern every existing provider uses (see `internal/provider/localprovider/factory.go` for the simplest real example).

In `internal/provider/<newbackend>/factory.go`:

```go
// ConfigProvider is the config dependency this provider needs — implemented
// by configAdapter in internal/provider/all/all.go.
type ConfigProvider interface {
	NewBackends() []config.NewBackend
	NewBackendSlicePtr() *[]config.NewBackend
	SetNewBackends([]config.NewBackend)
}

func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(newbackendCRUD{cfg: cfg}) // enables web CRUD for this backend kind
	return newbackendFactory{cfg: cfg}
}

type newbackendFactory struct{ cfg ConfigProvider }

// searchrun.ProviderFactory (required):
func (f newbackendFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend { /* ... */ }
func (f newbackendFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend      { /* ... */ }
func (f newbackendFactory) BackendRows() []config.BackendRow                        { /* ... */ }

// searchrun.BackendConfigRegistry (required for web CRUD / schema to find this backend kind):
func (f newbackendFactory) BackendKind() string { return "newbackend" }
func (f newbackendFactory) BackendSlicePtr() any { return f.cfg.NewBackendSlicePtr() }

var (
	_ searchrun.ProviderFactory       = newbackendFactory{}
	_ searchrun.BackendConfigRegistry = newbackendFactory{}
)
```

Then, in `internal/provider/all/all.go`:

1. Add `NewBackends()`, `NewBackendSlicePtr()`, and `SetNewBackends(...)` methods to `configAdapter`, each calling `config.Get()` (copy the `AWSBackends`/`AWSBackendSlicePtr`/`SetAWSBackends` trio as a template).
2. Append `newbackend.NewFactory(adapter)` to the slice returned by `Factories(deps Deps)`.

There is no blank-import/`init()` auto-registration to rely on — the factory only exists in the running binary once it's explicitly added to that slice.

## 3) Wire search/provider behavior

Implement `FromConfig`/`Default`/`BackendRows` in `internal/provider/<newbackend>` so:

- `honey search --provider <newbackend>` works
- `/api/v1/search` resolves this backend

If the backend should support the Docker auto-discover second pass (see [Docker auto-discover](./docker-auto-discover.md)), add a `DockerDiscover config.DockerDiscover` field to `NewBackend` and wrap the returned backend with `searchrun.WithDockerDiscover(backend, searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), b.DockerDiscover))` — see `localprovider/factory.go` for the exact pattern.

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

