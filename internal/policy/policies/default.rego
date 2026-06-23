package honey

import rego.v1

# Permissive default — honey ships allow-all so OPA is opt-in. Override by
# pointing HONEY_POLICY_DIR at your own .rego files (package honey), or replace
# this embedded default at build time.
default allow := true

default deny_reason := ""
