---
id: honey_macros
title: honey macros
---

## honey macros

Run predefined macros from a honeyfile manifest

```
honey macros [name] [flags]
```

### Examples

```
  # List all macros in the default honeyfile
  honey macros --list

  # Execute a macro by name
  honey macros restart-nginx

  # Show resolved configuration without executing
  honey macros restart-nginx --dry-run

  # Use a specific honeyfile
  honey macros --file /path/to/honeyfile.yaml --list
```

### Options

```
      --dry-run         Print resolved macro configuration and exit
      --file string     Path to honeyfile manifest (default: honeyfile.yaml or honeyfile.yml in current directory, or $HONEY_MACROS_FILE)
      --list            List all available macros
  -o, --output string   Output format: text or json (default "text")
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds
