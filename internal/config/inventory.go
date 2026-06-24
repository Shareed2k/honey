package config

import "github.com/shareed2k/honey/internal/hosts"

// InventoryValue is a scalar inventory variable value.
type InventoryValue = hosts.InventoryValue

// MustInventoryValue is for tests and static literals.
var MustInventoryValue = hosts.MustInventoryValue

// InventoryGroup defines variables applied to hosts matching a CEL expression.
type InventoryGroup struct {
	Priority int                       `yaml:"priority" json:"priority" honey:"label=Priority"`
	Match    string                    `yaml:"match" json:"match" honey:"label=CEL match expression" mod:"trim"`
	Vars     map[string]InventoryValue `yaml:"vars,omitempty" json:"vars,omitempty" honey:"label=Variables"`
}

// InventoryHost defines variables for a specific host.
type InventoryHost struct {
	Vars map[string]InventoryValue `yaml:"vars,omitempty" json:"vars,omitempty" honey:"label=Variables"`
}

// Inventory defines global variables and dynamic group matching rules.
type Inventory struct {
	Vars   map[string]InventoryValue `yaml:"vars,omitempty" json:"vars,omitempty" honey:"label=Global variables"`
	Groups map[string]InventoryGroup `yaml:"groups,omitempty" json:"groups,omitempty" honey:"label=Groups"`
	Hosts  map[string]InventoryHost  `yaml:"hosts,omitempty" json:"hosts,omitempty" honey:"label=Hosts"`
}
