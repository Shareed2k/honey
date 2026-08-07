// Controller test recipe: a system health check where the LLM decides which
// probes to run to satisfy the tasks, and settles a task from what it observes.
// All steps are safe, read-only, operator-local commands — runs anywhere with an
// OpenAI-compatible endpoint, no remote hosts or secrets needed.
//
//   honey cue-validate examples/recipe/controller_healthcheck.cue
//   honey cue-exec examples/recipe/controller_healthcheck.cue <inventory-host>            # dry-run (no LLM call)
//   OPENAI_API_KEY=… honey cue-exec examples/recipe/controller_healthcheck.cue <inventory-host> --execute
//
// (cue-exec needs the host search to return >=1 inventory host even though every
// step is operator-local; pass a selector that matches one.)
//
// What it exercises: the LLM runs load/memory/disk to satisfy "resources_reported",
// reads the disk output to decide "disk_pressure_assessed", and runs top_dirs only
// if the disk looks full (adaptive), then settles each task via finish.
recipe: {
	name: "controller-healthcheck"
	type: "controller"
	// Model resolves as: controller.model -> $OPENAI_MODEL -> "gpt-4o" default.
	// Left unset here so this example runs as-is against whatever endpoint you
	// point at: export OPENAI_MODEL to match (gpt-4o, gemini-2.5-flash, qwen2.5, …)
	// and set $OPENAI_BASE_URL for any OpenAI-compatible gateway (OpenAI, Gemini's
	// /v1beta/openai/, vLLM, Ollama). Pin controller.model instead if you want the
	// recipe to own the model regardless of env.
	controller: {max_turns: 12}
	tasks: [
		{name: "resources_reported", description: "CPU load, memory, and root-filesystem usage have each been observed and reported"},
		{name: "disk_pressure_assessed", description: "determined from the disk usage whether the root filesystem is under pressure (over 85% used); note the figure"},
	]
	steps: [
		{id: "load", description: "show CPU load averages", host: "_", command: "uptime"},
		{id: "memory", description: "show memory usage", host: "_", command: "free -h || vm_stat"},
		{id: "disk", description: "show root filesystem usage", host: "_", command: "df -h /"},
		{id: "top_dirs", description: "list the largest directories in the current path (run only if the disk looks full)", host: "_", command: "du -sh ./* 2>/dev/null | sort -h | tail -5"},
	]
}
