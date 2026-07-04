package honey

import rego.v1

# Restrict the "honey-app" service identity to reading the backends inventory
# and searching hosts over the REST API. Every other actor is unaffected — they
# fall through to the default allow below. Change "honey-app" to your app's
# JWT `sub` (or trusted-proxy user).
#
# honey evaluates every authenticated REST request through this policy as:
#   input = {action: "api_request", actor: <id>, method: <verb>, path: <path>}
#
# Enable:
#   export HONEY_POLICY_DIR=examples/policy/app-backends
#   export HONEY_JWT_PUBLIC_KEY=/path/to/jwt_pub.pem   # so actor == JWT `sub`
# or in the honey config:
#   defaults:
#     policy_dir: examples/policy/app-backends

default allow := true

app_actor := "honey-app"

# Read-only endpoints the app is permitted to call.
app_may_read if {
	input.method == "GET"
	input.path == "/api/v1/backends"
}

app_may_read if {
	input.method == "POST"
	input.path == "/api/v1/search"
}

# Deny the app actor any API request outside its allowed read endpoints. Write
# surfaces (e.g. POST /api/v1/config/backends/{kind}) are not in app_may_read,
# so they are denied for this actor.
allow := false if {
	input.action == "api_request"
	input.actor == app_actor
	not app_may_read
}

deny_reason := sprintf("%q may only read backends and search hosts", [app_actor]) if {
	input.action == "api_request"
	input.actor == app_actor
	not app_may_read
}
