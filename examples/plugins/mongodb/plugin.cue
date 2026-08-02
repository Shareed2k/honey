actions: query: {
	#Config: {
		uri:        string
		database:   string
		collection: string
		query:      string
	}
	
	argv: [
		"mongosh",
		config.uri,
		"--quiet",
		"--eval",
		"EJSON.stringify(db.getSiblingDB('\(config.database)').getCollection('\(config.collection)').find(\(config.query)).toArray())"
	]
	
	output_format: "json"
}

actions: eval: {
	#Config: {
		uri:    string
		script: string
	}
	
	argv: [
		"mongosh",
		config.uri,
		"--quiet",
		"--eval",
		config.script
	]
	
	output_format: "text"
}
