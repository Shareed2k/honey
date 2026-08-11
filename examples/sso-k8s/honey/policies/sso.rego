package honey

import rego.v1

# ---------------------------------------------------------------------------
# identity: the "role". Maps verified SSO claims to a Kubernetes identity that
# honey bakes into the short-lived client certificate at login time.
#   input = {action:"identity", target:"kube"|"ssh", cluster, subject, email,
#            groups, claims}
#   output = {user, groups, principals}
# ---------------------------------------------------------------------------

# A member of the IdP group "eng" becomes Kubernetes user <email>, group
# "honey-viewers" (which the demo RBAC binds to a pods-viewer ClusterRole), and
# — for SSH — principals [ubuntu, <email>].
identity := {
	"user": input.email,
	"groups": ["honey-viewers"],
	"principals": ["ubuntu", input.email],
} if {
	input.action == "identity"
	"eng" in input.groups
}

# ---------------------------------------------------------------------------
# k8s_request: per-request resource authorization on the proxy.
#   input = {action:"k8s_request", actor, cluster, groups, cluster_labels,
#            verb, resource, namespace, name, subresource}
# ---------------------------------------------------------------------------

# honey-viewers may read pods in the dev-labelled cluster, but never secrets.
allow if {
	input.action == "k8s_request"
	input.cluster_labels.env == "dev"
	"honey-viewers" in input.groups
	input.resource == "pods"
	input.verb in {"get", "list", "watch"}
}

deny_reason := "honey-viewers may not read secrets" if {
	input.action == "k8s_request"
	input.resource == "secrets"
}

# ---------------------------------------------------------------------------
# Defaults: deny both decisions unless a rule above fires (fail-closed).
# ---------------------------------------------------------------------------
default allow := false

# Allow the identity decision only when an identity was resolved.
allow if {
	input.action == "identity"
	identity
}
