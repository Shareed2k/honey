---
id: honey_egress
title: honey egress
---

## honey egress

Route traffic through a honey host via SOCKS5 (VPN-like exit)

### Synopsis

Establishes a SOCKS5 proxy over SSH to the named host.
All TCP connections routed through it will exit from that host.

With --tun, all system traffic is transparently routed (requires root and tun2proxy).
Use --bypass &lt;CIDR&gt; to exclude local subnets (e.g. local Docker ranges) from the tunnel.
Press Ctrl+C to stop.

```
honey egress &lt;host&gt; [flags]
```

### Options

```
      --auto-nets            Discover routable subnets on the remote host via SSH and route only those (requires --tun)
      --auto-proxy           Auto-configure system SOCKS5 proxy settings (macOS)
      --backends string      Comma-separated backend filter
      --bind string          Local bind address (default "127.0.0.1")
      --bypass stringArray   IP or CIDR to bypass the TUN tunnel (repeatable; loopback/link-local auto-excluded)
  -h, --help                 help for egress
      --nets stringArray     Route only these CIDRs through the tunnel (default: all traffic). Requires --tun. Repeatable.
      --pool-size int        Parallel SSH connections per host; &gt;1 increases throughput and reconnects on drop (default 1)
      --port int             Local SOCKS5 listen port (0 = random) (default 1080)
      --provider string      Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)
      --ssh-user string      SSH user override
      --tun                  Enable TUN mode: transparent VPN via tun2proxy (requires root)
```

### Options inherited from parent commands

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

