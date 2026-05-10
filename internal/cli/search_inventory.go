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

For CI, AWX, or Ansible Tower: install the honey binary where the job runs, inject credentials the same
way as for honey search (GCP ADC, AWS_* / profiles, KUBECONFIG, CONSUL_*, Proxmox env or HONEY_CONFIG YAML),
then point inventory at this command, for example:

  ansible-playbook -i 'honey inventory --config /path/to/honey.yaml --provider gcp --' site.yml

Use a trailing -- before playbook args if needed. AWX custom inventory script: set the script to honey
with arguments inventory --list (and optional --config / --provider / --backends as needed).`,
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
