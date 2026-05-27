package macros

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

const APIVersionV1Alpha1 = "honey.shareed2k.io/v1alpha1"

type MacroSet struct {
	APIVersion string     `yaml:"apiVersion" validate:"required,eq=honey.shareed2k.io/v1alpha1"`
	Kind       string     `yaml:"kind" validate:"required,eq=MacroSet"`
	Metadata   Metadata   `yaml:"metadata" validate:"required"`
	Spec       MacroSpecs `yaml:"spec" validate:"required"`
}

type Metadata struct {
	Name string `yaml:"name" validate:"required"`
}

type MacroSpecs struct {
	Macros map[string]Macro `yaml:"macros" validate:"required,min=1,dive,keys,required,endkeys,required"`
}

type Macro struct {
	Kind string `yaml:"kind" validate:"required,oneof=exec recipe logs tunnel app"`

	Target    string `yaml:"target"`
	Provider  string `yaml:"provider"`
	Backends  string `yaml:"backends"`
	Name      string `yaml:"name"`
	NameRegex string `yaml:"nameRegex"`

	Command  string   `yaml:"command"`
	Commands []string `yaml:"commands"`
	Parallel int      `yaml:"parallel" validate:"omitempty,gte=1"`
	Retry    int      `yaml:"retry" validate:"omitempty,gte=1"`
	Timeout  string   `yaml:"timeout" validate:"omitempty,goduration"`
	Shell    string   `yaml:"shell" validate:"omitempty,oneof=auto sh bash raw powershell"`
	RunAs    string   `yaml:"runAs"`
	Output   string   `yaml:"output" validate:"omitempty,oneof=text json"`
	Quiet    *bool    `yaml:"quiet"`

	RecipePath string   `yaml:"recipePath"`
	Execute    *bool    `yaml:"execute"`
	Env        []string `yaml:"env"`

	Source         string   `yaml:"source"`
	Unit           string   `yaml:"unit"`
	File           string   `yaml:"file"`
	Cmd            string   `yaml:"cmd"`
	Follow         *bool    `yaml:"follow"`
	Tail           *int64   `yaml:"tail" validate:"omitempty,gte=1"`
	Since          string   `yaml:"since" validate:"omitempty,goduration"`
	Timestamps     *bool    `yaml:"timestamps"`
	Grep           string   `yaml:"grep"`
	Labels         []string `yaml:"labels"`
	TUI            *bool    `yaml:"tui"`
	OutputFile     string   `yaml:"outputFile"`
	MaxConcurrency *int     `yaml:"maxConcurrency" validate:"omitempty,gte=1"`

	App         string `yaml:"app"`
	OpenBrowser *bool  `yaml:"openBrowser"`
}

func ResolvePath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Abs(strings.TrimSpace(explicit))
	}
	if env := strings.TrimSpace(os.Getenv("HONEY_MACROS_FILE")); env != "" {
		return filepath.Abs(env)
	}
	for _, n := range []string{"honeyfile.yaml", "honeyfile.yml"} {
		if _, err := os.Stat(n); err == nil {
			return filepath.Abs(n)
		}
	}
	return "", fmt.Errorf("honey macros file not found (use --file or HONEY_MACROS_FILE)")
}

func Load(path string) (*MacroSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ms MacroSet
	if err := yaml.Unmarshal(b, &ms); err != nil {
		return nil, err
	}
	v := validator.New(validator.WithRequiredStructEnabled())
	_ = v.RegisterValidation("goduration", func(fl validator.FieldLevel) bool {
		s := strings.TrimSpace(fl.Field().String())
		if s == "" {
			return true
		}
		_, err := time.ParseDuration(s)
		return err == nil
	})
	if err := v.Struct(ms); err != nil {
		return nil, err
	}
	for name, m := range ms.Spec.Macros {
		if err := validateMacro(name, m); err != nil {
			return nil, err
		}
	}
	return &ms, nil
}

func validateMacro(name string, m Macro) error {
	switch m.Kind {
	case "exec":
		hasSingle := strings.TrimSpace(m.Command) != ""
		hasMulti := len(m.Commands) > 0
		if strings.TrimSpace(m.Target) == "" || (!hasSingle && !hasMulti) {
			return fmt.Errorf("macro %q: exec requires target and command or commands", name)
		}
		if hasSingle && hasMulti {
			return fmt.Errorf("macro %q: exec supports either command or commands, not both", name)
		}
		for i, c := range m.Commands {
			if strings.TrimSpace(c) == "" {
				return fmt.Errorf("macro %q: commands[%d] must be non-empty", name, i)
			}
		}
	case "recipe":
		if strings.TrimSpace(m.Target) == "" || strings.TrimSpace(m.RecipePath) == "" {
			return fmt.Errorf("macro %q: recipe requires target and recipePath", name)
		}
	case "logs":
		if strings.TrimSpace(m.Target) == "" {
			return fmt.Errorf("macro %q: logs requires target", name)
		}
	case "tunnel", "app":
		if strings.TrimSpace(m.App) == "" {
			return fmt.Errorf("macro %q: %s requires app", name, m.Kind)
		}
	}
	if strings.EqualFold(m.Shell, "powershell") && strings.TrimSpace(m.RunAs) != "" {
		return fmt.Errorf("macro %q: runAs is not supported with powershell shell", name)
	}
	return nil
}
