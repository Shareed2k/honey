# Copyright (c) Honey contributors
# SPDX-License-Identifier: MIT
"""Ansible inventory plugin: runs ``honey inventory --list``"""

from __future__ import annotations

import json
import os
import re
import subprocess
from typing import Any

from ansible.errors import AnsibleParserError
from ansible.plugins.inventory import BaseInventoryPlugin

try:
    from ansible.module_utils.common.text.converters import to_native
except ImportError:  # pragma: no cover - very old Ansible
    def to_native(s, errors="surrogate_or_strict", nonstring="simplerepr"):  # type: ignore
        return str(s)


DOCUMENTATION = r"""
    name: honey
    plugin_type: inventory
    short_description: Use honey CLI as a dynamic inventory source
    version_added: "0.0.1"
    description:
        - Builds inventory by running C(honey inventory --list) and parsing JSON (same output as C(honey inventory) without C(--host)).
        - Requires the C(honey) binary on the Ansible controller (where C(ansible-playbook) runs).
    options:
      honey_binary:
        description: Path to the C(honey) executable.
        type: str
        default: honey
        env:
          - name: HONEY_BINARY
      config:
        description: Optional honey YAML path (C(--config)).
        type: str
        env:
          - name: HONEY_CONFIG
      provider:
        description: Optional C(--provider) value (e.g. C(gcp) or C(gcp,aws)).
        type: str
      backends:
        description: Optional C(--backends) comma-separated backend names.
        type: str
      name:
        description: Optional name substring (same as the C(honey inventory) positional filter).
        type: str
      args:
        description: Extra arguments appended before C(--list) (list of strings), e.g. C(['--ssh-user', 'deploy']).
        type: list
        elements: str
        default: []
      timeout:
        description: Seconds to wait for C(honey inventory). Set to C(0) to disable (OS default).
        type: int
        default: 300
        env:
          - name: HONEY_ANSIBLE_TIMEOUT
      strip_prefix:
        description: Remove 'honey_' prefix from Ansible groups and host variables. Group names will directly use tag/label values.
        type: bool
        default: False
      blacklist:
        description: Comma-separated list of tags or label keys to ignore (e.g. 'webserver,label_env')
        type: str
"""


class InventoryModule(BaseInventoryPlugin):
    NAME = "honey"

    def verify_file(self, path: str) -> bool:
        if not super().verify_file(path):
            return False
        if not path.endswith((".yml", ".yaml")):
            return False
        try:
            with open(path, encoding="utf-8") as fh:
                head = fh.read(512)
        except OSError:
            return False
        return bool(re.search(r"^\s*plugin:\s*honey\b", head, re.MULTILINE))

    def parse(self, inventory, loader, path, cache=True):
        try:
            super().parse(inventory, loader, path, cache=cache)
        except TypeError:
            super().parse(inventory, loader, path)
        self._read_config_data(path)

        cmd = self._command()
        timeout = self.get_option("timeout")
        if os.environ.get("HONEY_ANSIBLE_TIMEOUT"):
            try:
                timeout = int(os.environ["HONEY_ANSIBLE_TIMEOUT"].strip())
            except ValueError:
                pass
        kwargs: dict[str, Any] = {
            "capture_output": True,
            "text": True,
            "check": True,
        }
        if timeout:
            kwargs["timeout"] = int(timeout)

        try:
            proc = subprocess.run(cmd, **kwargs)
        except FileNotFoundError as e:
            raise AnsibleParserError(
                "honey inventory plugin: executable not found: %s (%s)"
                % (cmd[0], to_native(e))
            ) from e
        except subprocess.TimeoutExpired as e:
            raise AnsibleParserError(
                "honey inventory plugin: timeout after %ss: %s"
                % (timeout, " ".join(cmd))
            ) from e
        except subprocess.CalledProcessError as e:
            err = (e.stderr or e.stdout or "").strip() or to_native(e)
            raise AnsibleParserError(
                "honey inventory plugin: command failed (%s): %s"
                % (" ".join(cmd), err)
            ) from e

        try:
            payload = json.loads(proc.stdout)
        except json.JSONDecodeError as e:
            raise AnsibleParserError(
                "honey inventory plugin: invalid JSON from honey: %s" % to_native(e)
            ) from e

        self._populate(payload)

    def _command(self) -> list[str]:
        binary = self.get_option("honey_binary") or "honey"
        cmd: list[str] = [binary, "inventory"]

        cfg = self.get_option("config")
        if cfg:
            cmd.extend(["--config", cfg])

        prov = self.get_option("provider")
        if prov:
            cmd.extend(["--provider", prov])

        be = self.get_option("backends")
        if be:
            cmd.extend(["--backends", be])

        extra = self.get_option("args") or []
        if extra:
            cmd.extend([str(x) for x in extra])

        strip_prefix = self.get_option("strip_prefix")
        if strip_prefix:
            cmd.append("--strip-prefix")

        blacklist = self.get_option("blacklist")
        if blacklist:
            cmd.extend(["--blacklist", blacklist])

        cmd.append("--list")

        name = self.get_option("name")
        if name:
            cmd.append(name)

        return cmd

    def _populate(self, data: dict[str, Any]) -> None:
        if not isinstance(data, dict):
            raise AnsibleParserError("honey inventory JSON must be an object at the top level")

        meta = data.get("_meta") or {}
        if not isinstance(meta, dict):
            meta = {}
        hostvars = meta.get("hostvars") or {}
        if not isinstance(hostvars, dict):
            hostvars = {}

        for group_name, body in data.items():
            if group_name == "_meta":
                continue
            if not isinstance(body, dict):
                continue
            self.inventory.add_group(group_name)
            for h in body.get("hosts") or []:
                if not h:
                    continue
                self.inventory.add_host(str(h), group=group_name)

        for hostname, vars_map in hostvars.items():
            if not hostname or not isinstance(vars_map, dict):
                continue
            for k, v in vars_map.items():
                self.inventory.set_variable(str(hostname), k, v)
