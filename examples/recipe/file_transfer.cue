// SFTP upload (put) and download (get). Relative local paths are relative to this file's directory.
//
//   hostctl cue-validate examples/recipe/file_transfer.cue
//   hostctl cue-exec examples/recipe/file_transfer.cue my-filter
//   hostctl cue-exec --execute examples/recipe/file_transfer.cue my-filter
//
// put: one local file copied to the same remote path on each target.
// get: one remote file per step. With multiple targets, get.local must be a directory
//     (trailing / or existing dir); each host writes <dir>/<name>_<basename(remote)>.
recipe: {
	name: "file-transfer-demo"
	steps: [
		{host: "*", put: {local: "./README.md", remote: "/tmp/hostctl-recipe-readme"}},
		{host: "10.0.0.1", get: {local: "./hostname.single", remote: "/etc/hostname"}},
	]
}
