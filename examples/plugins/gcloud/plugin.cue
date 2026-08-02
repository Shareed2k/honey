// Compute Engine Actions (Read, Start, Stop)
actions: compute_list: {
	#Config: {
		project?: string | *""
		zone?:    string | *""
	}
	argv: [
		"gcloud", "compute", "instances", "list",
		if config.project != "" { "--project=\(config.project)" },
		if config.zone != ""    { "--zones=\(config.zone)" },
		"--format=json"
	]
	output_format: "json"
}

actions: compute_start: {
	#Config: {
		instance: string
		zone:     string
		project?: string | *""
	}
	argv: [
		"gcloud", "compute", "instances", "start",
		config.instance,
		"--zone=\(config.zone)",
		if config.project != "" { "--project=\(config.project)" },
		"--format=json"
	]
	output_format: "json"
}

actions: compute_stop: {
	#Config: {
		instance: string
		zone:     string
		project?: string | *""
	}
	argv: [
		"gcloud", "compute", "instances", "stop",
		config.instance,
		"--zone=\(config.zone)",
		if config.project != "" { "--project=\(config.project)" },
		"--format=json"
	]
	output_format: "json"
}

// Cloud Storage Actions (Read, Copy, Delete)
actions: storage_ls: {
	#Config: {
		url: string // e.g. gs://my-bucket/
	}
	argv: [
		"gcloud", "storage", "ls", config.url,
		"--format=json"
	]
	output_format: "json"
}

actions: storage_cp: {
	#Config: {
		source:      string
		destination: string
	}
	argv: [
		"gcloud", "storage", "cp",
		config.source,
		config.destination,
		"--format=json"
	]
	output_format: "json"
}

actions: storage_rm: {
	#Config: {
		url: string
	}
	argv: [
		"gcloud", "storage", "rm",
		config.url,
		"--format=json"
	]
	output_format: "json"
}
