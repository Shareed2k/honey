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
			command: "echo \"Running on $(hostname) with APP_ENV=$APP_ENV\""
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
			// Download a file from an exact host match
			host: "db-server-01"
			// Clear run_as to run as the SSH login user instead of root
			run_as: ""
			get: {
				remote: "/var/log/syslog"
				// Note: if downloading from multiple hosts, 'local' must be a directory
				local:  "downloads/syslog.txt" 
			}
		}
	]
}
