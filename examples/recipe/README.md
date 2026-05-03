# Honey Recipe Example

This directory contains an example [CUE](https://cuelang.org/) recipe that demonstrates how to automate multi-host deployments and execution using `honey cue-exec`.

## Files

- `example.cue`: The main recipe definition showcasing global defaults, variable injection, and different step kinds (`command`, `put`, `get`, `script`).
- `assets/index.html`: A dummy file used to demonstrate the `put` (upload) step.
- `scripts/setup.sh`: A shell script used to demonstrate the `script` (upload and execute) step.
- `downloads/`: An empty folder to receive files retrieved by the `get` step.

## How to use

You can run this recipe against your infrastructure by passing it to `honey cue-exec`. The CLI will first resolve your target hosts according to your search filters, and then safely resolve the steps.

By default, it runs in **dry-run** mode, outputting the plan:

```bash
# Preview what would happen on hosts matching "web-"
honey cue-exec examples/recipe/example.cue "web-"
```

When you are ready to apply the changes, append the `--execute` flag:

```bash
# Execute the recipe on matching hosts
honey cue-exec examples/recipe/example.cue "web-" --execute
```
