actions: fetch: {
	#Config: {
		url: string
	}
	
	argv: [
		"node", "/app/fetch.js", config.url
	]
	
	output_format: "json"
}
