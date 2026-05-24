// Rclone RC API via SSH local-forward tunnel (operator-side HTTP to tunneled rcd).
//
// Prerequisites on each target host:
//   - rclone on PATH; rcd_ensure starts rclone rcd on 127.0.0.1:5572 if not listening
//   - honey plugins.enabled with rclone module installed (make build-plugin-modules)
//   - plugins.network_allow_hosts includes 127.0.0.1
//
// Plan dry-run:
//   honey cue-exec examples/recipe/rclone_rc_tunnel.cue "role:app"
//
// Execute (requires live rcd + tunnel):
//   honey cue-exec -x examples/recipe/rclone_rc_tunnel.cue "role:app"
recipe: {
	name: "rclone-rc-tunnel"
	type: "graph"

	defaults: {
		secrets: {
			RCD_PASS: "secure:v1:FgI12SBS5O+c8jPf:75EGr8AkNa5A3wCjPfZ7363KgIS1"
		}
	}

	steps: [
		{
			id:   "rcd_ensure"
			host: "*"
			command: """
				ss -ltn | grep -q ':5572 ' || {
				  nohup rclone rcd --rc-addr 127.0.0.1:5572 --rc-user honey --rc-pass "${RCD_PASS}" >/tmp/rclone-rcd.log 2>&1 &
				  sleep 1
				}
				ss -ltn | grep -q ':5572 '
			"""
		},
		{
			id:      "rcd_tunnel"
			host:    "*"
			depends: ["rcd_ensure"]
			tunnel: {
				mode:        "local"
				remote_host: "127.0.0.1"
				remote_port: 5572
				share_key:   "rcd"
			}
		},
		{
			id:      "rc_list"
			host:    "*"
			depends: ["rcd_tunnel"]
			plugin: {
				id:     "rclone"
				action: "list"
				config: {
					tunnel_step: "rcd_tunnel"
					rc_user:     "honey"
					rc_pass:     "${RCD_PASS}"
					params: {
						fs:     "local:/tmp"
						remote: ""
					}
				}
			}
		},
	]
}
