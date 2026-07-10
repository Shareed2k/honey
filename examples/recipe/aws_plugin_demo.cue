// Demo recipe for the aws Docker plugin.
//
//   honey cue-exec --execute examples/recipe/aws_plugin_demo.cue <search-filter>
//
// Requires plugins.enabled: true and the aws plugin installed.
recipe: {
	name: "aws-plugin-demo"
	steps: [
		{
			host: "localhost" // AWS operations run locally
			name: "list-s3-buckets"
			plugin: {
				id:     "aws"
				action: "s3_ls"
				config: {
					bucket: "my-demo-bucket"
				}
			}
			register: "s3_data"
		},
		{
			host: "localhost"
			name: "describe-ec2"
			plugin: {
				id:     "aws"
				action: "ec2_describe"
				config: {
					tag_name: "production"
				}
			}
		}
	]
}
