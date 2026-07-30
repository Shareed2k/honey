package webserver

import (
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/metrics"
)

// FilesAPI owns the file-management HTTP endpoints (upload + local/remote file
// browsing, copy, agent transfer, stat/mkdir/remove, streamed up/download),
// isolating them from the main Server so the file feature carries its own
// dependencies (mirrors EnrollAPI/RecipesAPI, architecture candidate arch-08).
type FilesAPI struct {
	opts            Options
	metrics         *metrics.Registry
	fileClientCache *engine.ClientCache
	sshUser         func(string) string
}

// NewFilesAPI wires the file subsystem's dependencies. sshUser is injected
// (Server.sshUser) because it is shared Server-wide, not owned by this module.
func NewFilesAPI(opts Options, m *metrics.Registry, fileClientCache *engine.ClientCache, sshUser func(string) string) *FilesAPI {
	return &FilesAPI{opts: opts, metrics: m, fileClientCache: fileClientCache, sshUser: sshUser}
}
