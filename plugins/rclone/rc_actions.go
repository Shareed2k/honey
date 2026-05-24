// Package main implements the honey rclone RC WASM plugin action registry.
package main

import "fmt"

// rcAction describes one rclone RC HTTP endpoint.
type rcAction struct {
	Path string
}

var rcActions = map[string]rcAction{
	"noop":        {Path: "core/noop"},
	"copy":        {Path: "sync/copy"},
	"sync":        {Path: "sync/sync"},
	"list":        {Path: "operations/list"},
	"about":       {Path: "core/about"},
	"move":        {Path: "sync/move"},
	"delete":      {Path: "operations/delete"},
	"mkdir":       {Path: "operations/mkdir"},
	"job_status":  {Path: "job/status"},
	"job_finish":  {Path: "job/stop"},
	"mount":       {Path: "mount/mount"},
	"unmount":     {Path: "mount/unmount"},
	"vfs_refresh": {Path: "vfs/refresh"},
	"vfs_stats":   {Path: "vfs/stats"},
}

func rcActionPath(action string) (string, error) {
	a, ok := rcActions[action]
	if !ok {
		return "", fmt.Errorf("unknown rclone action %q", action)
	}
	return a.Path, nil
}
