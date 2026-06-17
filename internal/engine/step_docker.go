package engine

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/cenkalti/backoff/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// StreamCueStepDocker ...
func (run *CueRun) streamCueStepDocker(ctx context.Context, _ int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	ds, _ := step.(*cuetry.DockerStep)
	if ds == nil || ds.Docker == nil {
		return fmt.Errorf("internal: docker step missing docker field")
	}
	execOne := func(r hosts.Record) HostExecResult {
		outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			res := HostExecResult{
				Name:     r.Name,
				IP:       r.PrimaryIP,
				Provider: r.Provider,
			}
			var mCli *client.Client
			var err error

			zap.L().Debug(
				"docker step starting",
				zap.String("action", ds.Docker.Action),
				zap.String("host_name", r.Name),
				zap.String("primary_ip", r.PrimaryIP),
			)

			if r.PrimaryIP == "-" || r.PrimaryIP == "127.0.0.1" || r.PrimaryIP == "localhost" {
				mCli, err = client.New(client.FromEnv)
			} else {
				sshUser := run.Params.SSHUser
				if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
					sshUser = u
				}
				honeyClient, dialErr := run.Cache.GetOrDial(sshUser, r)
				if dialErr != nil {
					res.Success = false
					res.ErrMsg = fmt.Sprintf("ssh dial error: %s", dialErr.Error())
					zap.L().Debug("docker step failed (ssh dial)", zap.Error(dialErr), zap.String("host_name", r.Name))
					return res
				}
				var sshClient *ssh.Client
				sshClient, err = LeafSSHFromClient(honeyClient)
				if err != nil {
					res.Success = false
					res.ErrMsg = err.Error()
					zap.L().Debug("docker step failed (leaf ssh)", zap.Error(err), zap.String("host_name", r.Name))
					return res
				}

				bc := dockerprovider.BackendConfig{
					SSHUser: sshUser,
					Socket:  "/var/run/docker.sock",
					RunAs:   cuetry.EffectiveRunAs(step.Base(), run.Params.Recipe.Defaults),
				}
				opts := dockerprovider.APIClientOptions{
					SSHUser:     sshUser,
					BorrowedSSH: sshClient,
					VMRecord:    &r,
				}
				mCli, err = dockerprovider.NewAPIClient(ctx, bc, opts)
			}

			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("moby client error: %s", err.Error())
				zap.L().Debug("docker step failed (moby client init)", zap.Error(err), zap.String("host_name", r.Name))
				return res
			}
			defer mCli.Close()

			outputStr, execErr := executeDockerSDKAction(ctx, mCli, ds.Docker, run.Params.RecipeDir)
			if execErr != nil {
				res.Success = false
				res.ErrMsg = execErr.Error()
				zap.L().Debug("docker step failed (sdk action)", zap.Error(execErr), zap.String("host_name", r.Name))
				return res
			}

			zap.L().Debug("docker step finished", zap.String("action", ds.Docker.Action), zap.String("host_name", r.Name))
			res.Success = true
			res.Output = outputStr
			return res
		})

		RecordMaxAttempts(attemptMax, outcome.Attempts)
		return outcome.Result
	}

	for _, target := range targets {
		ch <- execOne(target)
	}
	return nil
}

func executeDockerSDKAction(ctx context.Context, cli *client.Client, d *cuetry.RecipeStepDocker, recipeDir string) (string, error) {
	switch d.Action {
	case "build":
		return executeDockerBuild(ctx, cli, d.Build, recipeDir)
	case "push":
		return executeDockerPush(ctx, cli, d.Push)
	case "pull":
		return executeDockerPull(ctx, cli, d.Pull)
	case "run":
		return executeDockerRun(ctx, cli, d.Run)
	case "exec":
		return executeDockerExec(ctx, cli, d.Exec)
	case "stop":
		return executeDockerStop(ctx, cli, d.Stop)
	}
	return "", fmt.Errorf("unsupported docker action: %s", d.Action)
}

func executeDockerBuild(ctx context.Context, cli *client.Client, b *cuetry.DockerBuild, recipeDir string) (string, error) {
	tarReader, err := createTarArchive(filepath.Join(recipeDir, b.Context))
	if err != nil {
		return "", fmt.Errorf("tar build context: %w", err)
	}
	dockerfile := b.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	opts := client.ImageBuildOptions{
		Dockerfile: dockerfile,
		Tags:       b.Tags,
		Remove:     true,
	}
	resp, err := cli.ImageBuild(ctx, tarReader, opts)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	logBytes, _ := io.ReadAll(resp.Body)
	resultMap := map[string]any{
		"logs":     string(logBytes),
		"image_id": "completed",
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

func executeDockerPush(ctx context.Context, cli *client.Client, p *cuetry.DockerPush) (string, error) {
	resp, err := cli.ImagePush(ctx, p.Image, client.ImagePushOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()
	logBytes, _ := io.ReadAll(resp)
	resultMap := map[string]any{
		"logs":   string(logBytes),
		"status": "pushed",
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

func executeDockerPull(ctx context.Context, cli *client.Client, p *cuetry.DockerPull) (string, error) {
	resp, err := cli.ImagePull(ctx, p.Image, client.ImagePullOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()
	logBytes, _ := io.ReadAll(resp)
	resultMap := map[string]any{
		"logs":   string(logBytes),
		"status": "pulled",
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

func executeDockerRun(ctx context.Context, cli *client.Client, r *cuetry.DockerRun) (string, error) {
	config := &container.Config{
		Image: r.Image,
		Cmd:   r.Command,
	}
	for k, v := range r.Env {
		config.Env = append(config.Env, fmt.Sprintf("%s=%s", k, v))
	}
	hostConfig := &container.HostConfig{}
	createOpts := client.ContainerCreateOptions{
		Config:     config,
		HostConfig: hostConfig,
		Name:       r.Name,
	}

	resp, err := backoff.Retry(ctx, func() (client.ContainerCreateResult, error) {
		res, innerErr := cli.ContainerCreate(ctx, createOpts)
		if innerErr != nil {
			if strings.Contains(innerErr.Error(), "No such image") {
				pullResp, pullErr := cli.ImagePull(ctx, r.Image, client.ImagePullOptions{})
				if pullErr != nil {
					return client.ContainerCreateResult{}, fmt.Errorf("auto-pull failed for %q: %w", r.Image, pullErr)
				}
				// Drain the response to block until the image pull completes
				_, _ = io.Copy(io.Discard, pullResp)
				pullResp.Close()
				return client.ContainerCreateResult{}, innerErr // Retry ContainerCreate on next tick
			}
			return client.ContainerCreateResult{}, backoff.Permanent(innerErr)
		}
		return res, nil
	})
	if err != nil {
		return "", err
	}
	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return "", err
	}

	resultMap := map[string]any{
		"container_id": resp.ID,
	}
	if !r.Detach {
		out, err := cli.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
		if err == nil {
			defer out.Close()
			logBytes, _ := io.ReadAll(out)
			resultMap["logs"] = string(logBytes)
		}
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

func executeDockerExec(ctx context.Context, cli *client.Client, e *cuetry.DockerExec) (string, error) {
	idResp, err := cli.ExecCreate(ctx, e.Container, client.ExecCreateOptions{
		Cmd:          e.Command,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}
	resp, err := cli.ExecAttach(ctx, idResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()
	outBytes, _ := io.ReadAll(resp.Reader)
	resultMap := map[string]any{
		"container": e.Container,
		"output":    string(outBytes),
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

func executeDockerStop(ctx context.Context, cli *client.Client, s *cuetry.DockerStop) (string, error) {
	if _, err := cli.ContainerStop(ctx, s.Container, client.ContainerStopOptions{}); err != nil {
		return "", err
	}
	resultMap := map[string]any{
		"container_id": s.Container,
		"status":       "stopped",
	}
	res, _ := json.Marshal(resultMap)
	return string(res), nil
}

// RunCueStepDockerDry ...
func RunCueStepDockerDry(out io.Writer, recipe cuetry.Recipe, i int, step cuetry.Step, targets []hosts.Record) error {
	runAs := cuetry.EffectiveRunAs(step.Base(), recipe.Defaults)
	ds, _ := step.(*cuetry.DockerStep)
	action := ""
	if ds != nil && ds.Docker != nil {
		action = ds.Docker.Action
	}
	for _, target := range targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=docker action=%q name=%q %s provider=%s run_as=%q\n",
			i, action, target.Name, FormatTargetForDryRun(target), target.Provider, runAs)
	}
	return nil
}

func createTarArchive(srcDir string) (io.Reader, error) {
	absSrcDir, err := filepath.Abs(filepath.Clean(srcDir))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absSrcDir)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer root.Close()
		tw := tar.NewWriter(pw)
		defer tw.Close()

		err := filepath.Walk(absSrcDir, func(file string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(fi, fi.Name())
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(absSrcDir, file)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(relPath)
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				return nil
			}
			f, err := root.Open(relPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			_ = pw.CloseWithError(err)
		}
	}()
	return pr, nil
}
