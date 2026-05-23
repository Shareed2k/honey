//go:build wasip1 || wasm

package pluginpdk

import (
	"encoding/json"
	"errors"

	"github.com/extism/go-pdk"
)

//go:wasmimport extism:host/user remote_exec
func remoteExecHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user remote_upload
func remoteUploadHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user remote_download
func remoteDownloadHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user remote_stat
func remoteStatHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user template_render
func templateRenderHost(inputOffset uint64) uint64

// RemoteExec runs a script on the remote host via the remote_exec host function.
func RemoteExec(shell, script string) (RemoteExecOutput, error) {
	return callRemote[RemoteExecOutput](remoteExecHost, remoteExecInput{Shell: shell, Script: script})
}

// RemoteUpload uploads a local file to the remote host.
func RemoteUpload(localPath, remotePath, mode string) (RemoteUploadOutput, error) {
	return callRemote[RemoteUploadOutput](remoteUploadHost, remoteUploadInput{
		LocalPath:  localPath,
		RemotePath: remotePath,
		Mode:       mode,
	})
}

// RemoteUploadContent uploads in-memory content to a remote path.
func RemoteUploadContent(remotePath, content, mode string) (RemoteUploadOutput, error) {
	return callRemote[RemoteUploadOutput](remoteUploadHost, remoteUploadInput{
		RemotePath: remotePath,
		Content:    content,
		Mode:       mode,
	})
}

// RemoteStat returns metadata for a remote path.
func RemoteStat(path string) (RemoteStatOutput, error) {
	return callRemote[RemoteStatOutput](remoteStatHost, remoteStatInput{Path: path})
}

// RemoteDownload reads a remote file (size-capped by the host).
func RemoteDownload(remotePath string, maxBytes int64) (RemoteDownloadOutput, error) {
	return callRemote[RemoteDownloadOutput](remoteDownloadHost, remoteDownloadInput{
		RemotePath: remotePath,
		MaxBytes:   maxBytes,
	})
}

// TemplateRender evaluates a Go text/template on the host.
func TemplateRender(template string, data map[string]any) (TemplateRenderOutput, error) {
	return callRemote[TemplateRenderOutput](templateRenderHost, templateRenderInput{
		Template: template,
		Data:     data,
	})
}

func callRemote[T any](hostFn func(uint64) uint64, in any) (T, error) {
	var zero T
	mem, err := pdk.AllocateJSON(in)
	if err != nil {
		return zero, err
	}
	off := hostFn(mem.Offset())
	if off == 0 {
		return zero, errors.New("host function returned 0")
	}
	result := pdk.FindMemory(off)
	var out T
	if err := json.Unmarshal(result.ReadBytes(), &out); err != nil {
		return zero, err
	}
	return out, nil
}
