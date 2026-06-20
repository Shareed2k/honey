recipe: {
	name: "with_promts"
	type: "linear"

	schedules: {
        "every-night": {
            cron:     "0 2 * * *"
            timezone: "America/New_York"
            env: { BACKUP_TARGET: "prod-db" }
        }
    }

	webhooks: {
		// "on_push" is the webhook_name used in the URL later
		"on_push": {
			// (Optional) Require an Authorization header matching a resolved secret
			auth_secret: "secure:v1:XVkyVCK/wRnEHSEZ:ZQrqzSSClYTZZuM5GXn3FUwEzHY=" //env:WEBHOOK_SECRET
			
			// (Optional) Extract data from the JSON body using JSON paths (gjson syntax)
			// and inject them as environment variables into the recipe
			extract: {
				"COMMIT_HASH": "after"
				"REPO_NAME":   "repository.full_name"
				"PUSHER":      "pusher.name"
			}

			// (Optional) Run in the background and return a tracking ID immediately
			async: true
		}
	}

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
		{
			host: "*"
			command: """
				bash -s << 'EOF'
					echo "${ACTION} Начинаем выполнение сложного скрипта..."
					if [ -d "/etc" ]; then
						echo "Папка /etc существует!"
					fi
					
					for i in {1..3}; do
						echo "Итерация $i"
					done
				EOF
				"""
		},
		{
			host: "*"
			command: """
				import sys
				print("Привет из Python!")
				for i in range(3):
					print(f"Итерация {i}")
				"""
			interpreter: "python3"
		},
		{
			host: "*"
			command: """
				set -eo pipefail
				echo "Это работает в bash!"
				"""
			interpreter: "bash"
		}
	]
}
