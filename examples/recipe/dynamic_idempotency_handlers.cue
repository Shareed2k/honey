// examples/recipe/dynamic_idempotency_handlers.cue
// Demonstrates check_cmd for idempotency and handlers for reactive execution.
// Run: honey cue-exec examples/recipe/dynamic_idempotency_handlers.cue "*"
recipe: {
	name: "idempotency-and-handlers"
	type: "graph"
	steps: [
		{
			id: "install_nginx"
			host: "*"
			// Only run apt-get install if the binary doesn't exist
			check_cmd: "command -v nginx"
			command: "apt-get update && apt-get install -y nginx"
			run_as: "root"
			// If check_cmd fails (nginx is missing), command runs.
			// If command succeeds, the step is marked "Changed: true" and triggers this handler:
			notify_handler: ["restart_nginx"]
		},
		{
			id: "configure_nginx"
			host: "*"
			depends: ["install_nginx"]
			// Only copy if the file contents differ
			check_cmd: "echo 'd41d8cd98f00b204e9800998ecf8427e  /etc/nginx/sites-available/default' | md5sum -c"
			command: "echo 'server { listen 80; root /var/www/html; }' > /etc/nginx/sites-available/default"
			run_as: "root"
			notify_handler: ["restart_nginx"]
		}
	]
	// Handlers only run at the end of the recipe IF they were notified by a changed step
	handlers: [
		{
			id: "restart_nginx"
			host: "*"
			command: "systemctl restart nginx"
			run_as: "root"
		}
	]
}
