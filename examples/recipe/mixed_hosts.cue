// Combine * (all in search), re: (name subset), and a literal IP in one recipe.
// Exact instance names also work when they match exactly one search row.
//
// Literal IPs do not need to appear in search results.
recipe: {
	name: "mixed-targeting"
	steps: [
		{host: "*", command: "uname -s"},
		{host: "re:^app-.+$", command: "test -d /tmp && echo ok"},
		{host: "203.0.113.10", command: "echo one-off"},
	]
}
