package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	garmErrors "github.com/cloudbase/garm-provider-common/errors"
	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
	executionv010 "github.com/cloudbase/garm-provider-common/execution/v0.1.0"
	executionv011 "github.com/cloudbase/garm-provider-common/execution/v0.1.1"
	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/mockstore"
	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/truenasstore"
)

var (
	_ executionv010.ExternalProvider = (*externalProvider)(nil)
	_ executionv011.ExternalProvider = (*externalProvider)(nil)
)

var Version = "v0.0.0-dev"

type trueNASConfig struct {
	Host               string `json:"host"`
	Username           string `json:"username"`
	APIKeyEnv          string `json:"api_key_env"`
	Port               int    `json:"port,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

type config struct {
	Mode      string         `json:"mode"`
	StateFile string         `json:"state_file,omitempty"`
	TrueNAS   *trueNASConfig `json:"truenas,omitempty"`
}

type externalProvider struct {
	cfg          config
	configPath   string
	controllerID string
}

func newExternalProvider(configPath, controllerID string) (*externalProvider, error) {
	if strings.TrimSpace(controllerID) == "" {
		return nil, fmt.Errorf("controller ID is required: %w", garmErrors.ErrBadRequest)
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &externalProvider{cfg: cfg, configPath: configPath, controllerID: controllerID}, nil
}

func (p *externalProvider) manager(ctx context.Context) (*provider.Manager, func() error, error) {
	var backend provider.Client
	var closeBackend func() error

	switch p.cfg.Mode {
	case "mock":
		backend = mockstore.New(p.cfg.StateFile)
	case "truenas":
		apiKeyEnv := strings.TrimSpace(p.cfg.TrueNAS.APIKeyEnv)
		if apiKeyEnv == "" {
			apiKeyEnv = "TRUENAS_API_KEY"
		}
		apiKey := os.Getenv(apiKeyEnv)
		if strings.TrimSpace(apiKey) == "" {
			return nil, nil, fmt.Errorf("TrueNAS API key environment variable %q is empty", apiKeyEnv)
		}
		store, closer, err := truenasstore.Connect(ctx, truenasstore.Config{
			Host:               p.cfg.TrueNAS.Host,
			Username:           p.cfg.TrueNAS.Username,
			APIKey:             apiKey,
			Port:               p.cfg.TrueNAS.Port,
			InsecureSkipVerify: false,
		})
		if err != nil {
			return nil, nil, err
		}
		backend = store
		closeBackend = closer
	default:
		return nil, nil, fmt.Errorf("provider mode %q is not supported", p.cfg.Mode)
	}

	manager, err := provider.NewManager(backend, p.controllerID)
	if err != nil {
		if closeBackend != nil {
			_ = closeBackend()
		}
		return nil, nil, err
	}
	return manager, closeBackend, nil
}

func (p *externalProvider) CreateInstance(ctx context.Context, bootstrap garmParams.BootstrapInstance) (garmParams.ProviderInstance, error) {
	if err := validateBootstrapContract(bootstrap); err != nil {
		return garmParams.ProviderInstance{}, badRequest(err)
	}
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return garmParams.ProviderInstance{}, normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}

	instance, err := manager.Create(ctx, provider.Bootstrap{
		Name:        bootstrap.Name,
		OSType:      string(bootstrap.OSType),
		Arch:        string(bootstrap.OSArch),
		Flavor:      bootstrap.Flavor,
		PoolID:      bootstrap.PoolID,
		CallbackURL: bootstrap.CallbackURL,
		MetadataURL: bootstrap.MetadataURL,
		Token:       bootstrap.InstanceToken,
	})
	if err != nil {
		return garmParams.ProviderInstance{}, normalizeProviderError(err)
	}
	return toGARMInstance(instance), nil
}

func (p *externalProvider) DeleteInstance(ctx context.Context, instance string) error {
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	return normalizeProviderError(manager.Delete(ctx, instance))
}

func (p *externalProvider) GetInstance(ctx context.Context, instance string) (garmParams.ProviderInstance, error) {
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return garmParams.ProviderInstance{}, normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	got, err := manager.Get(ctx, instance)
	if err != nil {
		return garmParams.ProviderInstance{}, normalizeProviderError(err)
	}
	return toGARMInstance(got), nil
}

func (p *externalProvider) ListInstances(ctx context.Context, poolID string) ([]garmParams.ProviderInstance, error) {
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return nil, normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	instances, err := manager.List(ctx, poolID)
	if err != nil {
		return nil, normalizeProviderError(err)
	}
	out := make([]garmParams.ProviderInstance, 0, len(instances))
	for _, instance := range instances {
		out = append(out, toGARMInstance(instance))
	}
	return out, nil
}

func (p *externalProvider) RemoveAllInstances(ctx context.Context) error {
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	return normalizeProviderError(manager.RemoveAll(ctx))
}

func (p *externalProvider) Stop(ctx context.Context, instance string, force bool) error {
	_ = force
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	return normalizeProviderError(manager.Stop(ctx, instance))
}

func (p *externalProvider) Start(ctx context.Context, instance string) error {
	manager, closeBackend, err := p.manager(ctx)
	if err != nil {
		return normalizeProviderError(err)
	}
	if closeBackend != nil {
		defer func() { _ = closeBackend() }()
	}
	return normalizeProviderError(manager.Start(ctx, instance))
}

func (p *externalProvider) GetVersion(context.Context) string {
	return Version
}

func (p *externalProvider) GetSupportedInterfaceVersions(context.Context) []string {
	return []string{commonExecution.Version010, commonExecution.Version011}
}

func (p *externalProvider) ValidatePoolInfo(_ context.Context, image, flavor, providerConfig, extraspecs string) error {
	if strings.TrimSpace(providerConfig) == "" {
		return badRequest(errors.New("provider config path is required"))
	}
	cfg, err := loadConfig(providerConfig)
	if err != nil {
		return badRequest(fmt.Errorf("load provider config: %w", err))
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	if strings.TrimSpace(image) != "" && image != provider.RunnerImage {
		return badRequest(fmt.Errorf("image %q is not the fixed TrueNAS runner image", image))
	}
	if strings.TrimSpace(flavor) != "" && flavor != provider.FlavorLinuxGeneral {
		return badRequest(fmt.Errorf("flavor %q is not supported", flavor))
	}
	if err := validateEmptyExtraSpecs([]byte(extraspecs)); err != nil {
		return badRequest(err)
	}
	return nil
}

func (p *externalProvider) GetConfigJSONSchema(context.Context) (string, error) {
	return providerConfigSchema, nil
}

func (p *externalProvider) GetExtraSpecsJSONSchema(context.Context) (string, error) {
	return extraSpecsSchema, nil
}

func validateBootstrapContract(bootstrap garmParams.BootstrapInstance) error {
	if bootstrap.OSType != garmParams.Linux {
		return fmt.Errorf("os_type %q is unsupported", bootstrap.OSType)
	}
	if bootstrap.OSArch != garmParams.Amd64 {
		return fmt.Errorf("arch %q is unsupported", bootstrap.OSArch)
	}
	if bootstrap.Flavor != provider.FlavorLinuxGeneral {
		return fmt.Errorf("flavor %q is unsupported", bootstrap.Flavor)
	}
	if bootstrap.Image != provider.RunnerImage {
		return fmt.Errorf("image %q is not the fixed TrueNAS runner image", bootstrap.Image)
	}
	if err := validateRunnerToolContract(bootstrap); err != nil {
		return err
	}
	if !bootstrap.JitConfigEnabled {
		return errors.New("JIT runner configuration is required for the ephemeral TrueNAS profile")
	}
	if strings.TrimSpace(bootstrap.Name) == "" || strings.TrimSpace(bootstrap.PoolID) == "" {
		return errors.New("runner name and pool ID are required")
	}
	for field, value := range map[string]string{
		"repo_url":       bootstrap.RepoURL,
		"callback-url":   bootstrap.CallbackURL,
		"metadata-url":   bootstrap.MetadataURL,
		"instance-token": bootstrap.InstanceToken,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	for field, value := range map[string]string{
		"repo_url":     bootstrap.RepoURL,
		"callback-url": bootstrap.CallbackURL,
		"metadata-url": bootstrap.MetadataURL,
	} {
		if err := requireHTTPS(field, value); err != nil {
			return err
		}
	}
	if len(bootstrap.SSHKeys) != 0 {
		return errors.New("SSH key injection is not supported by the fixed container profile")
	}
	if len(bootstrap.CACertBundle) != 0 {
		return errors.New("custom CA bundles are not yet supported by the fixed container profile")
	}
	if bootstrap.ProxyConfig.HasProxy() {
		return errors.New("runner proxy configuration is not yet supported by the fixed container profile")
	}
	if bootstrap.UserDataOptions.DisableUpdatesOnBoot || bootstrap.UserDataOptions.EnableBootDebug || len(bootstrap.UserDataOptions.ExtraPackages) != 0 {
		return errors.New("VM/cloud-init user-data options are not supported by the container profile")
	}
	if err := validateEmptyExtraSpecs(bootstrap.ExtraSpecs); err != nil {
		return err
	}
	return nil
}

func requireHTTPS(field, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", field, err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must use an absolute https URL", field)
	}
	return nil
}

func validateEmptyExtraSpecs(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("extra specs must be a JSON object: %w", err)
	}
	if len(obj) != 0 {
		return errors.New("extra specs are not supported by the fixed MVP profile")
	}
	return nil
}

func loadConfig(path string) (config, error) {
	var cfg config
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	switch cfg.Mode {
	case "mock":
		if strings.TrimSpace(cfg.StateFile) == "" {
			return badRequest(errors.New("mock state_file is required"))
		}
		if cfg.TrueNAS != nil {
			return badRequest(errors.New("mock mode cannot include truenas configuration"))
		}
	case "truenas":
		if strings.TrimSpace(cfg.StateFile) != "" {
			return badRequest(errors.New("truenas mode cannot include mock state_file"))
		}
		if cfg.TrueNAS == nil {
			return badRequest(errors.New("truenas configuration is required for truenas mode"))
		}
		if strings.TrimSpace(cfg.TrueNAS.Host) == "" || strings.TrimSpace(cfg.TrueNAS.Username) == "" {
			return badRequest(errors.New("truenas host and username are required"))
		}
		if cfg.TrueNAS.Port < 0 || cfg.TrueNAS.Port > 65535 {
			return badRequest(fmt.Errorf("truenas port %d is invalid", cfg.TrueNAS.Port))
		}
		if cfg.TrueNAS.InsecureSkipVerify {
			return badRequest(errors.New("insecure TLS verification is not permitted for the provider live transport"))
		}
	default:
		return badRequest(fmt.Errorf("provider mode %q is not supported", cfg.Mode))
	}
	return nil
}

func normalizeProviderError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return fmt.Errorf("%w: %v", garmErrors.ErrNotFound, err)
	case errors.Is(err, provider.ErrForeign):
		return fmt.Errorf("%w: %v", garmErrors.ErrUnauthorized, err)
	case errors.Is(err, provider.ErrUnsupported), errors.Is(err, provider.ErrUnsafeOperation):
		return fmt.Errorf("%w: %v", garmErrors.ErrBadRequest, err)
	case errors.Is(err, provider.ErrActive):
		return fmt.Errorf("%w: %v", garmErrors.ErrUnprocessable, err)
	default:
		return err
	}
}

func badRequest(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", garmErrors.ErrBadRequest, err)
}

func toGARMInstance(instance provider.Instance) garmParams.ProviderInstance {
	return garmParams.ProviderInstance{
		ProviderID:    instance.ProviderID,
		Name:          instance.Name,
		OSType:        garmParams.Linux,
		OSName:        instance.OSName,
		OSVersion:     instance.OSVersion,
		OSArch:        garmParams.Amd64,
		Status:        toGARMStatus(instance.Status),
		ProviderFault: []byte(instance.ProviderFault),
	}
}

func toGARMStatus(status string) garmParams.InstanceStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return garmParams.InstanceRunning
	case "stopped":
		return garmParams.InstanceStopped
	case "crashed":
		return garmParams.InstanceError
	case "deploying":
		return garmParams.InstanceCreating
	default:
		return garmParams.InstanceStatusUnknown
	}
}

const providerConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["mode"],
  "properties": {
    "mode": {"type": "string", "enum": ["mock", "truenas"]},
    "state_file": {"type": "string", "minLength": 1},
    "truenas": {
      "type": "object",
      "additionalProperties": false,
      "required": ["host", "username"],
      "properties": {
        "host": {"type": "string", "minLength": 1},
        "username": {"type": "string", "minLength": 1},
        "api_key_env": {"type": "string", "minLength": 1, "default": "TRUENAS_API_KEY"},
        "port": {"type": "integer", "minimum": 0, "maximum": 65535},
        "insecure_skip_verify": {"const": false}
      }
    }
  },
  "allOf": [
    {
      "if": {"properties": {"mode": {"const": "mock"}}},
      "then": {"required": ["state_file"]}
    },
    {
      "if": {"properties": {"mode": {"const": "truenas"}}},
      "then": {"required": ["truenas"]}
    }
  ]
}`

const extraSpecsSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "description": "The fixed MVP execution profile does not accept provider-specific extra specs.",
  "maxProperties": 0,
  "additionalProperties": false
}`
