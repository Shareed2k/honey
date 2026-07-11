// Demo recipe for the aws Docker plugin.
//
//   honey cue-exec --execute examples/recipe/aws_plugin_demo.cue
//
// Requires plugins.enabled: true and the aws plugin installed. Steps target
// host: "_" (local-only) — runtime: docker plugins always execute in a
// container on the operator machine and never touch a target host, so no
// search backend or matching host record is needed at all.
recipe: {
	name: "aws-plugin-demo"
	steps: [
		{
			host: "_"
			plugin: {
				id:     "aws"
				action: "s3_ls"
				config: {
					bucket: "my-demo-bucket"
				}
			}
		},
		{
			host: "_"
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
