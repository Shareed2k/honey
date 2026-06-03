package ui

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

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"golang.org/x/crypto/ssh"
)

func streamCueStepDocker(ctx context.Context, run *cueRun, _ int, step cuetry.RecipeStep, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	if step.Docker == nil {
		return fmt.Errorf("internal: docker step missing docker field")
	}
	execOne := func(r hosts.Record) HostExecResult {
		outcome := runHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			res := HostExecResult{
				Name:     r.Name,
				IP:       r.PrimaryIP,
				Provider: r.Provider,
			}
			var mCli *client.Client
			var err error

			if r.PrimaryIP == "127.0.0.1" || r.PrimaryIP == "localhost" {
				mCli, err = client.New(client.FromEnv)
			} else {
				sshUser := run.SSHUser
				if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
					sshUser = u
				}
				honeyClient, dialErr := run.cache.GetOrDial(sshUser, r)
				if dialErr != nil {
					res.Success = false
					res.ErrMsg = fmt.Sprintf("ssh dial error: %s", dialErr.Error())
					return res
				}
				var sshClient *ssh.Client
				sshClient, err = leafSSHFromClient(honeyClient)
				if err != nil {
					res.Success = false
					res.ErrMsg = err.Error()
					return res
				}

				bc := dockerprovider.BackendConfig{
					SSHUser: sshUser,
					Socket:  "/var/run/docker.sock",
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
				return res
			}
			defer mCli.Close()

			outputStr, execErr := executeDockerSDKAction(ctx, mCli, step.Docker, run.RecipeDir)
			if execErr != nil {
				res.Success = false
				res.ErrMsg = execErr.Error()
				return res
			}

			res.Success = true
			res.Output = outputStr
			return res
		})

		recordMaxAttempts(attemptMax, outcome.Attempts)
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
		tarReader, err := createTarArchive(filepath.Join(recipeDir, d.Build.Context))
		if err != nil {
			return "", fmt.Errorf("tar build context: %w", err)
		}
		dockerfile := d.Build.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		opts := client.ImageBuildOptions{
			Dockerfile: dockerfile,
			Tags:       d.Build.Tags,
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
		b, _ := json.Marshal(resultMap)
		return string(b), nil

	case "push":
		resp, err := cli.ImagePush(ctx, d.Push.Image, client.ImagePushOptions{})
		if err != nil {
			return "", err
		}
		defer resp.Close()
		logBytes, _ := io.ReadAll(resp)
		resultMap := map[string]any{
			"logs":   string(logBytes),
			"status": "pushed",
		}
		b, _ := json.Marshal(resultMap)
		return string(b), nil

	case "pull":
		resp, err := cli.ImagePull(ctx, d.Pull.Image, client.ImagePullOptions{})
		if err != nil {
			return "", err
		}
		defer resp.Close()
		logBytes, _ := io.ReadAll(resp)
		resultMap := map[string]any{
			"logs":   string(logBytes),
			"status": "pulled",
		}
		b, _ := json.Marshal(resultMap)
		return string(b), nil

	case "run":
		config := &container.Config{
			Image: d.Run.Image,
			Cmd:   d.Run.Command,
		}
		for k, v := range d.Run.Env {
			config.Env = append(config.Env, fmt.Sprintf("%s=%s", k, v))
		}
		hostConfig := &container.HostConfig{}
		resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
			Config:     config,
			HostConfig: hostConfig,
			Name:       d.Run.Name,
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
		if !d.Run.Detach {
			out, err := cli.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
			if err == nil {
				defer out.Close()
				logBytes, _ := io.ReadAll(out)
				resultMap["logs"] = string(logBytes)
			}
		}
		b, _ := json.Marshal(resultMap)
		return string(b), nil

	case "exec":
		idResp, err := cli.ExecCreate(ctx, d.Exec.Container, client.ExecCreateOptions{
			Cmd:          d.Exec.Command,
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
			"container": d.Exec.Container,
			"output":    string(outBytes),
		}
		b, _ := json.Marshal(resultMap)
		return string(b), nil

	case "stop":
		if _, err := cli.ContainerStop(ctx, d.Stop.Container, client.ContainerStopOptions{}); err != nil {
			return "", err
		}
		resultMap := map[string]any{
			"container_id": d.Stop.Container,
			"status":       "stopped",
		}
		b, _ := json.Marshal(resultMap)
		return string(b), nil
	}
	return "", fmt.Errorf("unsupported action")
}

func runCueStepDockerDry(out io.Writer, recipe cuetry.Recipe, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	action := step.Docker.Action
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
