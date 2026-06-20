recipe: {
	name: "example-webhook"
	description: "An example recipe demonstrating how to use webhooks to trigger executions and parse JSON payloads."

	webhooks: {
		"github_push": {
			// (Optional) Enforce authentication by matching the "Authorization" HTTP header
			// against a secret resolved by the engine (e.g. from environment variables).
			auth_secret: "env:MY_WEBHOOK_SECRET"

			// (Optional) Extract fields from the incoming HTTP POST JSON body using
			// gjson syntax and inject them into the recipe as environment variables.
			extract: {
				"COMMIT_HASH": "after"
				"REPO_NAME":   "repository.full_name"
				"AUTHOR":      "pusher.name"
			}

			// (Optional) If true, the webhook responds immediately with an HTTP 202
			// and queues the execution in the background.
			async: false
		}
	}

	steps: [
		{
			// Target the localhost to run the script. In a real app, this might be
			// an application server targeted by 'target_regex' or 'target' in the app config.
			host: "localhost"

			// Declare the variables extracted from the webhook payload so CUE validation passes.
			env: {
				COMMIT_HASH: string | *""
				REPO_NAME:   string | *""
				AUTHOR:      string | *""
			}

			command: """
			echo "Received webhook trigger!"
			echo "Repository: \(env.REPO_NAME)"
			echo "Commit: \(env.COMMIT_HASH)"
			echo "Triggered by: \(env.AUTHOR)"
			"""
		}
	]
}
