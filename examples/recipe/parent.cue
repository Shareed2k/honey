recipe: {
	name: "parent"

	defaults: {
		prompts: {
			"PARENT_MSG": {
				description: "Message to pass to child"
				type:        "string"
				default:     "hello from parent"
				required:    true
			}
		}
	}

	steps: [
		{
			host: "*"
			command: """
				echo "Parent starting..."
			"""
		},
		{
			host: "*"
			recipe: {
				path: "sub_setup.cue"
				prompts: {
					"MSG": "hardcoded value from parent"
				}
			}
		},
		{
			host: "*"
			command: """
				echo "Parent done."
			"""
		}
	]
}
