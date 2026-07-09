// Package main is the honey-plugin-init HTTP shim that runs arbitrary argv commands
// in a container, capturing stdout/stderr and exit codes for the dockerTransport.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func main() {
	addr := flag.String("addr", ":49094", "listen address")
	flag.Parse()

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	http.HandleFunc("/call", handleCall)

	log.Printf(`{"level":"info","msg":"honey-plugin-init listening","addr":%q}`, *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil { // #nosec G114
		log.Fatalf(`{"level":"error","msg":"listen failed","error":%q}`, err.Error())
	}
}

func handleCall(w http.ResponseWriter, r *http.Request) {
	var req apiv1.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, apiv1.ExecResponse{Error: "decode request: " + err.Error()})
		return
	}
	writeJSON(w, runArgv(req))
}

func runArgv(req apiv1.ExecRequest) apiv1.ExecResponse {
	if len(req.Argv) == 0 {
		return apiv1.ExecResponse{Error: "argv is empty"}
	}
	cmd := exec.Command(req.Argv[0], req.Argv[1:]...) // #nosec G204
	if len(req.Env) > 0 {
		// Base on the container's own environment, then layer this call's
		// resolved secrets on top — scoped to this one child process, never
		// written to the container's own env or this argv, so it doesn't
		// show up in `ps`/`/proc/<pid>/cmdline`.
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	resp := apiv1.ExecResponse{Output: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		resp.ExitCode = 0
	case asExitError(err, &exitErr):
		resp.ExitCode = exitErr.ExitCode()
	default:
		resp.Error = err.Error()
	}
	return resp
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = ee
	return true
}

func writeJSON(w http.ResponseWriter, resp apiv1.ExecResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
