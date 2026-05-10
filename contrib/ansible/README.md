# Ansible integration (honey)

- **`inventory_plugins/honey.py`** — inventory plugin: point `ANSIBLE_INVENTORY_PLUGINS` here (or copy `honey.py` to `~/.ansible/plugins/inventory/`), then use `-i` with a YAML file that contains `plugin: honey` (see `honey.gcp.example.yml`). No shell wrapper required.
- **`honey.gcp.example.yml`** — example inventory config; copy and edit paths and options.

Full usage and the script-wrapper alternative are in the repository root **README** (Ansible inventory) and in [`examples/ansible/README.md`](../examples/ansible/README.md).
