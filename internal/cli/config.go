package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/charmbracelet/huh"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/searchrun"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage honey configuration",
	Long:  "Interactive menus to manage configuration files and backends.",
	RunE:  runConfig,
}

func init() {
	rootCmd.AddCommand(configCmd)
}

func getOrInitConfig() (string, *config.File, error) {
	cfgPath, _ := config.ResolvePath(flagConfig)
	var cfg *config.File
	var err error

	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load config: %w", err)
		}
	} else {
		// Initialize new config
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		cfgPath, err = safepath.JoinUnder(home, ".config", "honey", "config.yaml")
		if err != nil {
			return "", nil, err
		}
		cfg = &config.File{
			Version: 1,
		}
	}
	return cfgPath, cfg, nil
}

func runConfig(_ *cobra.Command, _ []string) error {
	cfgPath, cfg, err := getOrInitConfig()
	if err != nil {
		return err
	}

	for {
		var action string
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Manage Backends (%s)", cfgPath)).
					Options(
						huh.NewOption("Add new backend", "add"),
						huh.NewOption("Edit a backend", "edit"),
						huh.NewOption("Delete a backend", "delete"),
						huh.NewOption("Exit", "exit"),
					).
					Value(&action),
			),
		).Run()

		if err != nil {
			return err
		}

		switch action {
		case "add":
			if err := runAddBackend(cfgPath, cfg); err != nil {
				return err
			}
		case "edit":
			if err := runEditBackend(cfgPath, cfg); err != nil {
				return err
			}
		case "delete":
			if err := runDeleteBackend(cfgPath, cfg); err != nil {
				return err
			}
		case "exit":
			return nil
		}
	}
}

func runAddBackend(cfgPath string, cfg *config.File) error {
	handlers := searchrun.GetCRUDHandlers()
	opts := make([]huh.Option[string], 0, len(handlers))
	for _, h := range handlers {
		opts = append(opts, huh.NewOption(h.Name(), h.ID()))
	}

	var providerID string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select Provider").
				Options(opts...).
				Value(&providerID),
		),
	).Run()

	if err != nil {
		return err
	}

	handler := searchrun.GetCRUDHandler(providerID)
	if handler == nil {
		return fmt.Errorf("unknown provider %s", providerID)
	}

	if err := handler.Add(cfg); err != nil {
		return err
	}

	if err := cfg.Save(cfgPath); err != nil {
		return err
	}

	fmt.Printf("Successfully added %s backend to %s\n", handler.Name(), cfgPath)
	return nil
}

func runEditBackend(cfgPath string, cfg *config.File) error {
	handlers := searchrun.GetCRUDHandlers()
	var opts []huh.Option[string]
	for _, h := range handlers {
		opts = append(opts, h.ListOptions(cfg)...)
	}

	if len(opts) == 0 {
		fmt.Println("No backends configured yet.")
		return nil
	}

	var selection string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select Backend to Edit").
				Options(opts...).
				Value(&selection),
		),
	).Run()
	if err != nil {
		return err
	}

	providerID, idx := parseSelection(selection)
	handler := searchrun.GetCRUDHandler(providerID)
	if handler == nil {
		return fmt.Errorf("unknown provider %s", providerID)
	}

	if err := handler.Edit(cfg, idx); err != nil {
		return err
	}

	if err := cfg.Save(cfgPath); err != nil {
		return err
	}

	fmt.Printf("Successfully updated %s backend in %s\n", handler.Name(), cfgPath)
	return nil
}

func runDeleteBackend(cfgPath string, cfg *config.File) error {
	handlers := searchrun.GetCRUDHandlers()
	var opts []huh.Option[string]
	for _, h := range handlers {
		opts = append(opts, h.ListOptions(cfg)...)
	}

	if len(opts) == 0 {
		fmt.Println("No backends configured yet.")
		return nil
	}

	var selection string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select Backend to Delete").
				Options(opts...).
				Value(&selection),
		),
	).Run()
	if err != nil {
		return err
	}

	providerID, idx := parseSelection(selection)
	handler := searchrun.GetCRUDHandler(providerID)
	if handler == nil {
		return fmt.Errorf("unknown provider %s", providerID)
	}

	var confirm bool
	err = huh.NewConfirm().Title(fmt.Sprintf("Are you sure you want to delete this %s backend?", handler.Name())).Value(&confirm).Run()
	if err != nil || !confirm {
		return nil
	}

	if err := handler.Delete(cfg, idx); err != nil {
		return err
	}

	if err := cfg.Save(cfgPath); err != nil {
		return err
	}

	fmt.Printf("Successfully deleted %s backend from %s\n", handler.Name(), cfgPath)
	return nil
}
