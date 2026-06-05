// OpenSearch integration demo recipe.
// Demonstrates connecting to an OpenSearch/Elasticsearch cluster to:
// 1. Index a document (write data).
// 2. Retrieve a document by ID (get data).
// 3. Perform a search query and map fields to a subsequent deployment step (read data).
//
// Usage:
//   honey cue-exec examples/recipe/opensearch_demo.cue
//   honey cue-exec --execute examples/recipe/opensearch_demo.cue

recipe: {
	name: "opensearch-demo"
	type: "graph"

	steps: [
		{
			id:   "index_config"
			host: "_"
			opensearch: {
				addresses: ["http://localhost:9200"]
				index:  "app-config"
				action: "index"
				doc_id: "snapshot-1"
				body: {
					version:      "v2.1.4"
					download_url: "https://storage.googleapis.com/releases/v2.1.4.tar.gz"
					status:       "active"
				}
			}
		},
		{
			id:      "get_config"
			depends: ["index_config"]
			host:    "_"
			opensearch: {
				addresses: ["http://localhost:9200"]
				index:  "app-config"
				action: "get"
				doc_id: "snapshot-1"
				output: "retrieved_config"
			}
		},
		{
			id:      "search_configs"
			depends: ["index_config"]
			host:    "_"
			opensearch: {
				addresses: ["http://localhost:9200"]
				index:  "app-config"
				action: "search"
				body: {
					query: {
						match: { status: "active" }
					}
				}
				output: "active_configs"
			}
		},
		{
			id:      "deploy_app"
			depends: ["search_configs"]
			host:    "*"
			env_from: [{
				from_output: "active_configs"
				extract: {
					DOWNLOAD_URL: ".hits.hits[0]._source.download_url"
					APP_VERSION:  ".hits.hits[0]._source.version"
				}
			}]
			command: "echo 'Deploying version $APP_VERSION from $DOWNLOAD_URL'"
		}
	]
}
