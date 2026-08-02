// examples/plugins/audio_transcribe/plugin.cue
actions: list_new: {
	#Config: {}

	argv: [
		"python3", "/app/list_new.py"
	]

	output_format: "json"
}

actions: process_file: {
	#Config: {
		file: string
	}

	argv: [
		"python3", "/app/process_file.py", config.file
	]

	output_format: "json"
}
