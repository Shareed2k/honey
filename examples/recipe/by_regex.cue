// Match instance Name with a Go regexp (RE2). Only hosts with an IP are targeted.
// Use (?i) for case-insensitive matching.
//
//   honey cue-exec examples/recipe/by_regex.cue prod
// Adjust the pattern to match your inventory names from honey search.
recipe: {
	name: "subset-by-name-regex"
	steps: [
		{host: "re:^prod-kafka-\\d+$", command: "hostname"},
		{host: "re:(?i)^staging-.*-worker$", command: "date -u"},
	]
}
