package inventory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

type compiledGroup struct {
	name string
	grp  config.InventoryGroup
	prog cel.Program
}

// Apply resolves inventory groups and variables into each host record.
func Apply(records []hosts.Record, inv config.Inventory) error {
	groups, err := compileGroups(inv.Groups)
	if err != nil {
		return err
	}
	for i := range records {
		applyRecord(&records[i], inv, groups)
	}
	return nil
}

func compileGroups(groups map[string]config.InventoryGroup) ([]compiledGroup, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	env, err := cel.NewEnv(cel.Variable("host", cel.DynType))
	if err != nil {
		return nil, err
	}
	out := make([]compiledGroup, 0, len(groups))
	for name, grp := range groups {
		match := strings.TrimSpace(grp.Match)
		if match == "" {
			continue
		}
		ast, iss := env.Compile(match)
		if iss != nil && iss.Err() != nil {
			return nil, fmt.Errorf("inventory group %q match: %w", name, iss.Err())
		}
		prog, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("inventory group %q match: %w", name, err)
		}
		out = append(out, compiledGroup{name: name, grp: grp, prog: prog})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].grp.Priority == out[j].grp.Priority {
			return out[i].name < out[j].name
		}
		return out[i].grp.Priority < out[j].grp.Priority
	})
	return out, nil
}

func applyRecord(r *hosts.Record, inv config.Inventory, groups []compiledGroup) {
	vars := make(map[string]hosts.InventoryValue, len(inv.Vars))
	for k, v := range inv.Vars {
		vars[k] = v
	}
	matched := make([]string, 0)
	for _, group := range groups {
		ok, err := matchGroup(group, *r)
		if err != nil || !ok {
			continue
		}
		matched = append(matched, group.name)
		for k, v := range group.grp.Vars {
			vars[k] = v
		}
	}
	for _, key := range hostKeys(*r) {
		host, ok := inv.Hosts[key]
		if !ok {
			continue
		}
		for k, v := range host.Vars {
			vars[k] = v
		}
	}
	if len(vars) > 0 {
		r.Vars = vars
	}
	if len(matched) > 0 {
		r.Groups = matched
	}
}

func matchGroup(group compiledGroup, r hosts.Record) (bool, error) {
	out, _, err := group.prog.Eval(map[string]any{"host": hostToCELMap(r)})
	if err != nil {
		return false, err
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("match did not evaluate to bool")
	}
	return b, nil
}

func hostKeys(r hosts.Record) []string {
	keys := make([]string, 0, 2)
	if strings.TrimSpace(r.Name) != "" {
		keys = append(keys, r.Name)
	}
	if strings.TrimSpace(r.Provider) != "" && strings.TrimSpace(r.Name) != "" && strings.TrimSpace(r.PrimaryIP) != "" {
		keys = append(keys, fmt.Sprintf("%s/%s/%s", r.Provider, r.Name, r.PrimaryIP))
	}
	return keys
}

func hostToCELMap(r hosts.Record) map[string]any {
	meta := make(map[string]string, len(r.Meta))
	for k, v := range r.Meta {
		meta[k] = v
	}
	extra := make([]string, len(r.ExtraIPs))
	copy(extra, r.ExtraIPs)
	return map[string]any{
		"name":      r.Name,
		"ip":        r.PrimaryIP,
		"provider":  r.Provider,
		"zone":      r.Zone,
		"region":    r.Region,
		"meta":      meta,
		"extra_ips": extra,
	}
}
