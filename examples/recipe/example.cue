recipe: {
	name: "Example honey recipe"

	defaults: {
		// Run commands as root using sudo -n (unless overridden)
		//run_as: "root"
		
		// Environment variables available to all 'command' and 'script' steps
		env: {
			"APP_ENV": "production"
		}
		
		// Default image used when spinning up ephemeral debug containers in Kubernetes
		k8s_debug_image: "nicolaka/netshoot:latest"
	}

	steps: [
		{
			// Target all hosts from the search results
			host: "*"
			command: "echo \"Running on $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) with APP_ENV=$APP_ENV\""
		},
		{
			// Target hosts using a regular expression matched against the host name
			host: "re:redis/redis-.*"
			put: {
				local:  "assets/index.html"
				remote: "/tmp/index.html"
			}
		},
		{
			// Upload a local script and execute it
			host: "re:redis/redis-.*"
			script: {
				local:  "scripts/setup.sh"
				remote: "/tmp/setup.sh"
			}
			// Override default env for just this step
			env: {
				"APP_ENV": "staging"
			}
		},
		{
			// Loop dynamically generated via the injected 'hosts' variable
			// This generates one step per matching k8s pod
			for h in hosts if h.provider == "k8s" {
				host: h.name
				command: "echo \"Kubernetes Pod IP is \(h.primary_ip)\""
			}
		}
	]
}
