recipe: {
	name: "Example: Advanced Prompts (File, Password, Remote URL)"
	type: "linear"

	defaults: {
		prompts: {
			"DB_PASSWORD": {
				description: "Database admin password"
				type:        "password"
				required:    true
			}

			"CONFIG_FILE": {
				description: "Upload a JSON or YAML configuration file"
				type:        "file"
				required:    true
			}

			"JSON_PAYLOAD": {
				description: "Inline JSON payload for the API request"
				type:        "multiline"
				default:     "{\n  \"key\": \"value\"\n}"
				required:    true
			}

			"API_ENDPOINT": {
				description:       "Select a public API endpoint"
				type:              "string"
				choices_url:       "https://api.ipify.org/?format=json"
				choices_json_path: "$.ip"
				required:          true
			}
		}
	}

	steps: [
		{
			host: "*"
			command: """
				echo "--- Prompt Results ---"
				
				# Password type
				echo "Password length: ${#DB_PASSWORD} characters"
				# DO NOT echo passwords in real environments!
				
				# Multiline type
				echo "JSON Payload:"
				echo "${JSON_PAYLOAD}"
				
				# Remote URL choices type
				echo "Selected API Endpoint: ${API_ENDPOINT}"
				
				# File type variables:
				# HONEY_PROMPT_CONFIG_FILE: internal ID
				# HONEY_FILE_CONFIG_FILE: absolute path on the honey server
				# HONEY_FILE_CONFIG_FILE_FILENAME: original uploaded filename
				# HONEY_FILE_CONFIG_FILE_SHA: sha256 of the uploaded file
				
				echo "File uploaded: ${HONEY_FILE_CONFIG_FILE_FILENAME}"
				echo "File SHA256: ${HONEY_FILE_CONFIG_FILE_SHA}"
			"""
		},
		{
			// Example of processing the uploaded file on the local honey server before distributing it
			host: "_" // Special host representing the honey server itself
			command: """
				echo "Reading file contents from the honey server's local temp dir..."
				cat "${HONEY_FILE_CONFIG_FILE}"
			"""
		}
	]
}
