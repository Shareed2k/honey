# Ansible dynamic inventory with honey

## Option A: inventory plugin (no shell wrapper)

Use the **inventory plugin** from this repository so `-i` points at a **YAML file** (not a command line):

1. Put [`contrib/ansible/inventory_plugins/honey.py`](../../contrib/ansible/inventory_plugins/honey.py) on Ansible’s inventory plugin path, for example:
   - `export ANSIBLE_INVENTORY_PLUGINS="/absolute/path/to/honey/contrib/ansible/inventory_plugins"`, or  
   - copy `honey.py` into `~/.ansible/plugins/inventory/`.
2. Copy or symlink [`contrib/ansible/honey.gcp.example.yml`](../../contrib/ansible/honey.gcp.example.yml) next to your playbooks and edit `honey_binary`, `provider`, `config`, etc.
3. Run: `ansible-playbook -i ./honey.gcp.example.yml your_playbook.yml`

Ansible runs `honey inventory --list` under the hood; you do not pass a shell string to `-i`.

## Option B: executable wrapper (script inventory)

Ansible’s **script** inventory expects **one executable file on disk**. It is **not** a shell command string.

1. Copy [`honey_inventory_gcp.example.sh`](honey_inventory_gcp.example.sh) to a path (for example `/usr/local/bin/honey-inv-gcp`).
2. Set `HONEY` inside the script to your `honey` binary path, or put `honey` on `PATH` and keep the default.
3. `chmod +x` that file.
4. Run: `ansible-playbook -i /usr/local/bin/honey-inv-gcp your_playbook.yml`

Ansible will execute `honey-inv-gcp --list` (or `--host …`). The wrapper must end with `"$@"` so those arguments reach `honey inventory`.

For more flags (`--config`, `--backends`, name filter), edit the `exec` line in your copy or add a generic wrapper:

```sh
#!/bin/sh
exec /path/to/honey inventory "$@"
```

See the root **README** section **Ansible inventory (`honey inventory`)** for full details.
