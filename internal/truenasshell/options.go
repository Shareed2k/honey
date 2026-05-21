package truenasshell

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

type virtInstanceRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RecordSupportsAPIShell reports whether rec has the metadata shape for TrueNAS /websocket/shell.
func RecordSupportsAPIShell(rec hosts.Record) bool {
	return hosts.IsTrueNASAPIShellRecord(rec)
}

// shellOptionsSupported reports whether rec can use TrueNAS /websocket/shell (before API resolve).
func shellOptionsSupported(rec hosts.Record) error {
	kind := strings.ToLower(strings.TrimSpace(rec.Meta["kind"]))
	switch kind {
	case "appliance":
		return nil
	case "virt_instance":
		if strings.TrimSpace(rec.Meta["id"]) == "" {
			return fmt.Errorf("truenas virt_instance record missing id")
		}
		return nil
	case "vm":
		if strings.TrimSpace(rec.Meta["virt_instance_id"]) != "" {
			return nil
		}
		if strings.TrimSpace(rec.Name) == "" {
			return fmt.Errorf("truenas vm record missing name")
		}
		return nil
	default:
		return fmt.Errorf("truenas api shell: unsupported meta.kind %q", kind)
	}
}

// resolveShellOptions builds /websocket/shell options per middleware webshell_app.py (virt_instance_id for guests).
func resolveShellOptions(ctx context.Context, api *truenasprovider.Client, rec hosts.Record) (map[string]any, error) {
	if err := shellOptionsSupported(rec); err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(rec.Meta["kind"]))
	switch kind {
	case "appliance":
		return nil, nil
	case "virt_instance":
		return map[string]any{
			"virt_instance_id": strings.TrimSpace(rec.Meta["id"]),
		}, nil
	case "vm":
		if id := strings.TrimSpace(rec.Meta["virt_instance_id"]); id != "" {
			return map[string]any{"virt_instance_id": id}, nil
		}
		return resolveVirtInstanceIDByName(ctx, api, rec.Name)
	default:
		return nil, fmt.Errorf("truenas api shell: unsupported meta.kind %q", kind)
	}
}

func resolveVirtInstanceIDByName(ctx context.Context, api *truenasprovider.Client, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("truenas vm record missing name")
	}
	var rows []virtInstanceRow
	filters := []any{[]any{[]any{"name", "=", name}}}
	if err := api.Call(ctx, "virt.instance.query", filters, &rows); err != nil {
		return nil, fmt.Errorf("truenas virt.instance.query: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no virt.instance for VM %q; use a virt_instance row or SSH", name)
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("multiple virt.instance rows named %q", name)
	}
	id := strings.TrimSpace(rows[0].ID)
	if id == "" {
		return nil, fmt.Errorf("virt.instance %q has empty id", name)
	}
	return map[string]any{"virt_instance_id": id}, nil
}
