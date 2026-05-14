// Remote recipe: free disk space on typical Ubuntu/systemd + snap hosts.
//
// WARNING — journald: Step 3 runs `journalctl --vacuum-time=1s`, which removes
// almost all persisted journal history on the host. Only use when that loss
// of logs is acceptable. Hosts without journalctl skip journal steps cleanly.
//
// Snap: Removes only *disabled* revisions (old revs kept after refresh). Each
// `snap remove` may still fail (e.g. race); failures are ignored so the step
// completes (`|| true` on each remove). Hosts without snap skip that step.
//
// Requires passwordless `sudo -n` for root on the remote (defaults.run_as).
//
// Validate:
//   honey cue-validate examples/recipe/clean_filesystem.cue
// Plan (dry-run):
//   honey cue-exec examples/recipe/clean_filesystem.cue "<search>"
// Apply:
//   honey cue-exec examples/recipe/clean_filesystem.cue "<search>" --execute
// (From repo root without install: go run ./cmd/honey … same args.)
//
// Targeting: `host: "*"` runs on every search result row with an IP. Use a
// literal name/IP or `re:…` per step instead if you need narrower targets.
recipe: {
	name: "clean-filesystem-journal-snap"

	defaults: {
		run_as: "root"
	}

	steps: [
		{
			host: "*"
			command: "command -v journalctl >/dev/null 2>&1 && journalctl --disk-usage || echo \"skip: no journalctl\""
		},
		{
			host: "*"
			command: "command -v journalctl >/dev/null 2>&1 && journalctl --rotate && journalctl --vacuum-time=1s || echo \"skip: no journalctl\""
		},
		{
			host: "*"
			// LANG=C for stable snap list columns; awk fields: name=$1 rev=$3 on disabled lines.
			command: "(command -v snap >/dev/null 2>&1 && { LANG=C snap list --all 2>/dev/null | awk '$1 != \"Name\" && /disabled/ {print $1, $3}' | while read -r name rev; do [ -z \"$rev\" ] || snap remove \"$name\" --revision=\"$rev\" || true; done; }) || echo \"skip: no snap or snap list failed\""
		},
		{
			host: "*"
			command: "test -d /var/lib/snapd/cache && find /var/lib/snapd/cache/ -type f -delete || echo \"skip: no snapd cache dir\""
		},
		{
			host: "*"
			command: "rm -f /tmp/*.tar.gz /tmp/*.deb"
		}
	]
}
