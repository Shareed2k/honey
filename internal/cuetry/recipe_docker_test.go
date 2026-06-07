package cuetry

import (
	"testing"
)

func TestClassifyDockerStep(t *testing.T) {
	step := &DockerStep{
		StepBase: StepBase{Host: "localhost"},
		Docker: &RecipeStepDocker{
			Action: "build",
			Build: &DockerBuild{
				Context: "./app",
			},
		},
	}
	if err := step.Validate(StepValidateCtx{}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if step.Kind() != KindDocker {
		t.Errorf("expected KindDocker, got %v", step.Kind())
	}
}

func TestParseRemoteRecipe_dockerBuildOk(t *testing.T) {
	const src = `
recipe: {
	name: "docker-build-test"
	steps: [
		{
			host: "localhost"
			docker: {
				action: "build"
				build: {
					context: "./app"
					dockerfile: "./app/Dockerfile"
					tags: ["my-app:1.0"]
					build_args: {
						ENV: "prod"
					}
				}
			}
		}
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("failed to parse valid docker build recipe: %v", err)
	}
	if len(r.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(r.Steps))
	}
	step := r.Steps[0].Step.(*DockerStep)
	if step.Docker == nil || step.Docker.Action != "build" {
		t.Fatalf("parsed step is missing correct docker config: %+v", step.Docker)
	}
	if step.Docker.Build.Context != "./app" || step.Docker.Build.Tags[0] != "my-app:1.0" {
		t.Errorf("incorrect build context or tags: %+v", step.Docker.Build)
	}
}

func TestParseRemoteRecipe_dockerRunOk(t *testing.T) {
	const src = `
recipe: {
	name: "docker-run-test"
	steps: [
		{
			host: "localhost"
			docker: {
				action: "run"
				output: "run_out"
				run: {
					image: "nginx:latest"
					name: "my-nginx"
					ports: ["80:80"]
					volumes: ["/var/www:/usr/share/nginx/html"]
					env: {
						PORT: "80"
					}
					detach: true
				}
			}
		}
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("failed to parse valid docker run recipe: %v", err)
	}
	step := r.Steps[0].Step.(*DockerStep)
	if step.Docker == nil || step.Docker.Action != "run" {
		t.Fatalf("parsed step is missing correct docker config")
	}
	if step.Docker.Run.Image != "nginx:latest" || step.Docker.Run.Name != "my-nginx" || !step.Docker.Run.Detach {
		t.Errorf("incorrect run config: %+v", step.Docker.Run)
	}
}

func TestParseRemoteRecipe_dockerMultipleActions(t *testing.T) {
	const src = `
recipe: {
	name: "docker-bad"
	steps: [
		{
			host: "localhost"
			docker: {
				action: "build"
				build: {
					context: "./app"
				}
				push: {
					image: "my-image:latest"
				}
			}
		}
	]
}
`
	_, err := ParseRemoteRecipe([]byte(src), nil)
	if err == nil {
		t.Fatal("expected error when defining multiple actions in a single docker step")
	}
}

func TestParseRemoteRecipe_dockerInvalidAction(t *testing.T) {
	const src = `
recipe: {
	name: "docker-bad"
	steps: [
		{
			host: "localhost"
			docker: {
				action: "invalid-action-name"
			}
		}
	]
}
`
	_, err := ParseRemoteRecipe([]byte(src), nil)
	if err == nil {
		t.Fatal("expected error when using invalid action name")
	}
}
