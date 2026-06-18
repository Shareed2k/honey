recipe: {
	name: "Example: Rundeck-style Prompts"
	type: "linear"

	defaults: {
		prompts: {
			// A simple text input with a default value
			"SERVICE_NAME": {
				description: "Name of the service to operate on"
				type:        "string"
				default:     "nginx"
				required:    true
			}

			// A dropdown/enum using choices
			"ACTION": {
				description: "Action to perform on the service"
				type:        "string"
				choices: [
					"restart",
					"stop",
					"start",
					"status",
				]
				default:  "status"
				required: true
			}

			// A multiple selection dropdown
			"ENV_TARGETS": {
				description: "Which environments to target (comma-separated output)"
				type:        "string"
				choices: [
					"prod",
					"staging",
					"dev",
					"qa",
				]
				multi:    true
				default:  "staging,qa"
				required: true
			}

			// A boolean switch/checkbox
			"FORCE_MODE": {
				description: "Apply action forcefully?"
				type:        "boolean"
				default:     "false"
				required:    true
			}

			// A text field with regex validation (e.g. valid IP address)
			"NOTIFY_IP": {
				description: "IP address to ping after completion"
				type:        "string"
				regex:       "^([0-9]{1,3}\\.){3}[0-9]{1,3}$"
				default:     "127.0.0.1"
				required:    true
			}
		}
	}

	steps: [
		{
			host: "*"
			command: """
				echo "Operating on service: ${SERVICE_NAME}"
				echo "Action: ${ACTION}"
				echo "Environments: ${ENV_TARGETS}"
				echo "Force Mode: ${FORCE_MODE}"
				echo "Notify IP: ${NOTIFY_IP}"
				"""
		},
	]
}
