// S3 Actions (Read, Create/Update, Delete)
actions: s3_ls: {
	#Config: {
		bucket: string
	}
	argv: [
		"aws", "s3api", "list-objects-v2",
		"--bucket", config.bucket,
		"--output", "json"
	]
	output_format: "json"
}

actions: s3_cp: {
	#Config: {
		source:      string
		destination: string
	}
	argv: [
		"aws", "s3", "cp",
		config.source,
		config.destination,
		"--output", "json"
	]
	output_format: "json"
}

actions: s3_rm: {
	#Config: {
		bucket: string
		key:    string
	}
	argv: [
		"aws", "s3api", "delete-object",
		"--bucket", config.bucket,
		"--key", config.key,
		"--output", "json"
	]
	output_format: "json"
}

// EC2 Actions (Read, Start, Stop)
actions: ec2_describe: {
	#Config: {
		tag_name?: string | *"" 
	}
	argv: [
		"aws", "ec2", "describe-instances",
		if config.tag_name != "" { "--filters" },
		if config.tag_name != "" { "Name=tag:Name,Values=\(config.tag_name)" },
		"--output", "json"
	]
	output_format: "json"
}

actions: ec2_start: {
	#Config: {
		instance_ids: [...string]
	}
	argv: [
		"aws", "ec2", "start-instances",
		"--instance-ids"
	] + config.instance_ids + [
		"--output", "json"
	]
	output_format: "json"
}

actions: ec2_stop: {
	#Config: {
		instance_ids: [...string]
	}
	argv: [
		"aws", "ec2", "stop-instances",
		"--instance-ids"
	] + config.instance_ids + [
		"--output", "json"
	]
	output_format: "json"
}
