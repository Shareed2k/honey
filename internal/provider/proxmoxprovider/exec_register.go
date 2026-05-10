package proxmoxprovider

import "github.com/shareed2k/honey/internal/hostexec"

func init() {
	hostexec.RegisterProxmoxExecutor(resolveProxmoxExecutor)
}
