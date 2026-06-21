recipe: {
	name: "sub_setup"
	
	defaults: {
		prompts: {
			"MSG": {
				description: "Message to echo"
				type:        "string"
				default:     "hello child"
				required:    true
			}
		}
	}

	steps: [
		{
			host: "*"
			command: """
				echo "Sub recipe says: ${MSG}"
			"""
		}
	]
}
