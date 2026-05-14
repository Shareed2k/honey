---
id: honey_mcp
title: honey mcp
---

## honey mcp

Run the Model Context Protocol (stdio) server

### Synopsis

Starts the MCP server on stdin/stdout for Cursor, Claude Desktop, and other MCP clients.

Only stderr may be used for logging; stdout carries the JSON-RPC stream.

```
honey mcp [flags]
```

### Options

```
  -h, --help   help for mcp
```

### Options inherited from parent commands

```
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

