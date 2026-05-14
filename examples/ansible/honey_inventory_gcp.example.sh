#!/bin/sh
# Copy or symlink to a path you pass to ansible-playbook -i, then chmod +x.
# Ansible runs: this_script --list
#           or: this_script --host <inventory_hostname>
# Always forward "$@" so those reach honey.
#
# Edit HONEY to your honey binary (e.g. /tmp/honey, $(command -v honey), or a release path).
HONEY="${HONEY:-honey}"
exec "$HONEY" inventory --provider gcp "$@"
