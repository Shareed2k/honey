package dockerprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

func mergeVMNodeMeta(meta map[string]string, vm hosts.Record, discover bool) map[string]string {
	out := make(map[string]string, len(meta)+8)
	for k, v := range meta {
		out[k] = v
	}
	for k, v := range vmNodeMeta(vm, discover) {
		out[k] = v
	}
	return out
}

func vmNodeMeta(vm hosts.Record, discover bool) map[string]string {
	meta := make(map[string]string)
	if name := strings.TrimSpace(vm.Name); name != "" {
		meta["docker_vm"] = name
		if discover {
			meta["docker_discover_vm"] = name
		}
	}
	if ip := strings.TrimSpace(vm.PrimaryIP); ip != "" {
		meta["docker_vm_ip"] = ip
		if discover {
			meta["docker_discover_vm_ip"] = ip
		}
	}
	if ext := vm.ExternalIP(); ext != "" {
		meta["docker_vm_external_ip"] = ext
		if discover {
			meta["docker_discover_vm_external_ip"] = ext
		}
	}
	if p := strings.TrimSpace(vm.Provider); p != "" {
		meta["via_provider"] = p
		if discover {
			meta["docker_discover_source"] = p
		}
	}
	if b := strings.TrimSpace(vm.Meta["backend_name"]); b != "" && discover {
		meta["docker_discover_backend"] = b
	}
	if discover {
		meta["docker_discover"] = "1"
	}
	return meta
}

func applyVMNodeRecordFields(rec *hosts.Record, vm hosts.Record) {
	if rec == nil {
		return
	}
	internal := strings.TrimSpace(vm.PrimaryIP)
	external := vm.ExternalIP()
	if external != "" {
		rec.PrimaryIP = external
		if internal != "" && internal != external {
			rec.ExtraIPs = []string{internal}
		}
		return
	}
	if internal != "" {
		rec.PrimaryIP = internal
	}
}
