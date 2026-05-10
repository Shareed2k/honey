package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/inventory"
)

var inventoryCmd = &cobra.Command{
	Use:   "inventory [name]",
	Short: "Print Ansible-compatible JSON dynamic inventory from the same search as honey search",
	Long: `Runs the same parallel discovery as honey search (all search flags apply), then prints JSON
suitable for Ansible's script inventory plugin: with --list (or no --host), a top-level object
with a honey group, optional honey_provider_*, honey_region_*, honey_zone_* groups, and _meta.hostvars.

Each host gets ansible_host from the discovered PrimaryIP when present, ansible_user from --ssh-user
(or config defaults.ssh_user), plus honey_* variables and honey_meta_* keys from record meta.

Ansible's -i flag takes a path to an inventory file or directory (or multiple paths), not a shell command.
For script inventory, use a small executable wrapper that runs "honey inventory ..." and forwards "$@".

To avoid a wrapper, use the YAML inventory plugin in contrib/ansible/inventory_plugins/honey.py
(see contrib/ansible/honey.gcp.example.yml and examples/ansible/README.md).

For CI, AWX, or Ansible Tower: install honey, inject credentials like honey search, then either set
ANSIBLE_INVENTORY_PLUGINS to that directory and use a plugin YAML with -i, or use a wrapper script.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInventory,
}

func init() {
	rootCmd.AddCommand(inventoryCmd)
	inventoryCmd.Flags().AddFlagSet(searchCmd.Flags())
	inventoryCmd.Flags().Bool("list", false, "Ansible script inventory: emit full JSON (Ansible passes this; optional when not using --host)")
	inventoryCmd.Flags().String("host", "", "Ansible script inventory: emit JSON object of host variables for this inventory name; unknown hosts print {}")
}

func runInventory(cmd *cobra.Command, args []string) error {
	records, sshUser, _, _, err := runSearchCore(cmd, args)
	if err != nil {
		return err
	}
	if records == nil {
		records = []hosts.Record{}
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")

	if h, _ := cmd.Flags().GetString("host"); h != "" {
		hv, err := inventory.AnsibleHostVars(records, sshUser, h)
		if err != nil {
			return enc.Encode(map[string]any{})
		}
		return enc.Encode(hv)
	}

	out := inventory.AnsibleList(records, sshUser)
	return enc.Encode(out)
}
