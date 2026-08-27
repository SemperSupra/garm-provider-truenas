package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/SemperSupra/garm-provider-truenas/internal/mockstore"
	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type config struct {
	Mode      string `json:"mode"`
	StateFile string `json:"state_file"`
}

func main() {
	if err := run(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdin io.Reader, stdout, _ io.Writer) error {
	command := os.Getenv("GARM_COMMAND")
	controllerID := os.Getenv("GARM_CONTROLLER_ID")
	configPath := os.Getenv("GARM_PROVIDER_CONFIG_FILE")
	if command == "" || controllerID == "" || configPath == "" {
		return errors.New("GARM_COMMAND, GARM_CONTROLLER_ID and GARM_PROVIDER_CONFIG_FILE are required")
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if cfg.Mode != "mock" {
		return fmt.Errorf("provider mode %q is not enabled; live TrueNAS transport remains fail-closed until protocol qualification is complete", cfg.Mode)
	}
	if cfg.StateFile == "" {
		return errors.New("mock state_file is required")
	}

	manager, err := provider.NewManager(mockstore.New(cfg.StateFile), controllerID)
	if err != nil {
		return err
	}

	switch command {
	case "CreateInstance":
		var in provider.Bootstrap
		if err := json.NewDecoder(stdin).Decode(&in); err != nil {
			return fmt.Errorf("decode bootstrap: %w", err)
		}
		instance, err := manager.Create(ctx, in)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(instance)

	case "GetInstance":
		instance, err := manager.Get(ctx, os.Getenv("GARM_INSTANCE_ID"))
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(instance)

	case "ListInstances":
		instances, err := manager.List(ctx, os.Getenv("GARM_POOL_ID"))
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(instances)

	case "DeleteInstance":
		return manager.Delete(ctx, os.Getenv("GARM_INSTANCE_ID"))

	case "Start":
		return manager.Start(ctx, os.Getenv("GARM_INSTANCE_ID"))

	case "Stop":
		return manager.Stop(ctx, os.Getenv("GARM_INSTANCE_ID"))

	case "RemoveAllInstances":
		return manager.RemoveAll(ctx)

	default:
		return fmt.Errorf("unknown GARM_COMMAND %q", command)
	}
}

func loadConfig(path string) (config, error) {
	var cfg config
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
