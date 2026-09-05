package truenasstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	truenas "github.com/deevus/truenas-go"
	tnclient "github.com/deevus/truenas-go/client"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

const (
	labelManaged    = "io.sempersupra.garm.managed"
	labelSchema     = "io.sempersupra.garm.schema"
	labelController = "io.sempersupra.garm.controller-id"
	labelPool       = "io.sempersupra.garm.pool-id"
	labelProfile    = "io.sempersupra.garm.execution-profile"
	metadataSchema  = "1"
)

var errUnmanaged = errors.New("app is not managed by garm-provider-truenas")

type Config struct {
	Host                string
	Username            string
	APIKey              string
	Port                int
	InsecureSkipVerify  bool
	CallbackHostGateway bool
}

type appService interface {
	CreateApp(context.Context, truenas.CreateAppOpts) (*truenas.App, error)
	GetAppWithConfig(context.Context, string) (*truenas.App, error)
	ListApps(context.Context) ([]truenas.App, error)
}

type jobCaller interface {
	CallAndWait(context.Context, string, any) (json.RawMessage, error)
}

type Store struct {
	apps                appService
	caller              jobCaller
	callbackHostGateway bool
}

func Connect(ctx context.Context, cfg Config) (*Store, func() error, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, nil, errors.New("TrueNAS host is required")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, nil, errors.New("TrueNAS username is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, nil, errors.New("TrueNAS API key is required")
	}
	if cfg.InsecureSkipVerify {
		return nil, nil, errors.New("insecure TLS verification is not permitted for the provider live transport")
	}

	ws, err := tnclient.NewWebSocketClient(tnclient.WebSocketConfig{
		Host:               cfg.Host,
		Username:           cfg.Username,
		APIKey:             cfg.APIKey,
		Port:               cfg.Port,
		InsecureSkipVerify: false,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure TrueNAS WebSocket client: %w", err)
	}
	if err := ws.Connect(ctx); err != nil {
		_ = ws.Close()
		return nil, nil, fmt.Errorf("connect TrueNAS JSON-RPC transport: %w", err)
	}

	apps := truenas.NewAppService(ws, ws.Version())
	store := New(apps, ws)
	store.callbackHostGateway = cfg.CallbackHostGateway
	return store, ws.Close, nil
}

func New(apps appService, caller jobCaller) *Store {
	return &Store{apps: apps, caller: caller}
}

func (s *Store) CreateApp(ctx context.Context, spec provider.AppSpec) (provider.App, error) {
	if s == nil || s.apps == nil {
		return provider.App{}, errors.New("TrueNAS app service is required")
	}
	compose, err := composeConfig(spec)
	if err != nil {
		return provider.App{}, err
	}
	compose, err = applyCallbackHostGateway(compose, spec, s.callbackHostGateway)
	if err != nil {
		return provider.App{}, err
	}
	if _, err := s.apps.CreateApp(ctx, truenas.CreateAppOpts{
		Name:                spec.Name,
		CustomApp:           true,
		CustomComposeConfig: compose,
	}); err != nil {
		return provider.App{}, err
	}
	return s.GetApp(ctx, spec.Name)
}

func (s *Store) GetApp(ctx context.Context, name string) (provider.App, error) {
	if s == nil || s.apps == nil {
		return provider.App{}, errors.New("TrueNAS app service is required")
	}
	app, err := s.apps.GetAppWithConfig(ctx, name)
	if err != nil {
		return provider.App{}, err
	}
	if app == nil {
		return provider.App{}, provider.ErrNotFound
	}
	decoded, err := decodeApp(*app)
	if errors.Is(err, errUnmanaged) {
		return provider.App{}, provider.ErrForeign
	}
	if err != nil {
		return provider.App{}, err
	}
	if err := validateCallbackHostGatewayAppConfig(*app, s.callbackHostGateway); err != nil {
		return provider.App{}, fmt.Errorf("managed app callback host-gateway policy mismatch: %w", err)
	}
	return decoded, nil
}

func plausibleManagedAppName(name string) bool {
	if !strings.HasPrefix(name, "garm-") {
		return false
	}
	remainder := strings.TrimPrefix(name, "garm-")
	controller, requested, ok := strings.Cut(remainder, "-")
	return ok && strings.TrimSpace(controller) != "" && strings.TrimSpace(requested) != ""
}

func (s *Store) ListApps(ctx context.Context) ([]provider.App, error) {
	if s == nil || s.apps == nil {
		return nil, errors.New("TrueNAS app service is required")
	}
	apps, err := s.apps.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	out := make([]provider.App, 0, len(apps))
	for _, summary := range apps {
		if !plausibleManagedAppName(summary.Name) {
			continue
		}
		full, err := s.apps.GetAppWithConfig(ctx, summary.Name)
		if err != nil {
			return nil, err
		}
		if full == nil {
			continue
		}
		decoded, err := decodeApp(*full)
		if errors.Is(err, errUnmanaged) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("decode managed app %q: %w", summary.Name, err)
		}
		if err := validateCallbackHostGatewayAppConfig(*full, s.callbackHostGateway); err != nil {
			return nil, fmt.Errorf("validate managed app %q callback host-gateway policy: %w", summary.Name, err)
		}
		out = append(out, decoded)
	}
	return out, nil
}

func (s *Store) DeleteApp(ctx context.Context, name string) error {
	if s == nil || s.caller == nil {
		return errors.New("TrueNAS job caller is required")
	}
	// Keep deletion explicitly non-volume-destructive. The provider manager has
	// already proven ownership and inactive state before this call is reachable.
	_, err := s.caller.CallAndWait(ctx, "app.delete", []any{
		name,
		map[string]any{
			"remove_images":     false,
			"remove_ix_volumes": false,
		},
	})
	return err
}

func composeConfig(spec provider.AppSpec) (string, error) {
	if spec.Image != provider.RunnerImage || spec.ExecutionProfile != provider.FlavorLinuxGeneral {
		return "", fmt.Errorf("image/profile is outside the allowlisted runner profile: %w", provider.ErrUnsupported)
	}
	if spec.CPU != provider.GeneralCPU || spec.MemoryBytes != provider.GeneralMemoryBytes {
		return "", fmt.Errorf("resource request is outside the allowlisted runner profile: %w", provider.ErrUnsupported)
	}
	if spec.RunAsUser != "1001:1001" || !spec.NoNewPrivileges || spec.WorkdirTmpfs || !spec.CredentialTmpfs {
		return "", fmt.Errorf("runtime security profile is not the fixed MVP profile: %w", provider.ErrUnsupported)
	}
	if spec.RunnerDownloadURL != provider.RunnerToolURL || spec.RunnerFilename != provider.RunnerToolFilename || spec.RunnerSHA256 != provider.RunnerToolSHA256 {
		return "", fmt.Errorf("runner payload is outside the allowlisted verified tool contract: %w", provider.ErrUnsupported)
	}
	if len(spec.HostMounts) != 0 || spec.DockerSocket {
		return "", fmt.Errorf("host mounts and Docker socket are forbidden: %w", provider.ErrUnsupported)
	}
	if len(spec.CapDrop) != 1 || spec.CapDrop[0] != "ALL" {
		return "", fmt.Errorf("all Linux capabilities must be dropped: %w", provider.ErrUnsupported)
	}

	labels := map[string]string{
		labelManaged:    "true",
		labelSchema:     metadataSchema,
		labelController: spec.ControllerID,
		labelPool:       spec.PoolID,
		labelProfile:    spec.ExecutionProfile,
	}

	// JSON is valid YAML and gives us deterministic, injection-safe scalar
	// encoding without introducing a second Compose/YAML implementation.
	compose := map[string]any{
		"services": map[string]any{
			"runner": map[string]any{
				"image":      spec.Image,
				"user":       spec.RunAsUser,
				"restart":    "no",
				"entrypoint": []string{"/bin/sh", "-c", containerBootstrapCommand()},
				"cap_drop": []string{
					"ALL",
				},
				"security_opt": []string{"no-new-privileges:true"},
				"cpus":         spec.CPU,
				"mem_limit":    spec.MemoryBytes,
				// Docker/TrueNAS mounts tmpfs with noexec. Use that property only
				// for the JIT credential bytes. Runner binaries and _work stay on
				// the one-job container writable layer so Actions can execute files.
				"tmpfs": []string{
					"/run/garm-jit:rw,nosuid,nodev,noexec,uid=1001,gid=1001,mode=0700",
				},
				"labels": labels,
				"environment": map[string]string{
					"GARM_CALLBACK_URL":        spec.CallbackURL,
					"GARM_METADATA_URL":        spec.MetadataURL,
					"GARM_INSTANCE_TOKEN":      spec.BootstrapToken,
					"GARM_RUNNER_DOWNLOAD_URL": spec.RunnerDownloadURL,
					"GARM_RUNNER_FILENAME":     spec.RunnerFilename,
					"GARM_RUNNER_SHA256":       spec.RunnerSHA256,
				},
			},
		},
	}
	encoded, err := json.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("encode fixed runner Compose: %w", err)
	}
	return string(encoded), nil
}

func decodeApp(app truenas.App) (provider.App, error) {
	compose, err := composeObject(app.Config)
	if err != nil {
		return provider.App{}, err
	}
	services, ok := object(compose["services"])
	if !ok {
		return provider.App{}, errors.New("managed app config has no services object")
	}
	runner, ok := object(services["runner"])
	if !ok {
		return provider.App{}, errors.New("managed app config has no runner service")
	}
	labels, err := labelMap(runner["labels"])
	if err != nil {
		return provider.App{}, err
	}
	if labels[labelManaged] != "true" {
		return provider.App{}, errUnmanaged
	}
	if labels[labelSchema] != metadataSchema {
		return provider.App{}, fmt.Errorf("unsupported ownership metadata schema %q", labels[labelSchema])
	}
	controllerID := strings.TrimSpace(labels[labelController])
	poolID := strings.TrimSpace(labels[labelPool])
	profile := strings.TrimSpace(labels[labelProfile])
	if controllerID == "" || poolID == "" || profile == "" {
		return provider.App{}, errors.New("managed app ownership metadata is incomplete")
	}
	image, _ := runner["image"].(string)
	if image != provider.RunnerImage || profile != provider.FlavorLinuxGeneral {
		return provider.App{}, errors.New("managed app drifted from the fixed image/profile")
	}

	return provider.App{
		Spec: provider.AppSpec{
			Name:              app.Name,
			Image:             image,
			ControllerID:      controllerID,
			PoolID:            poolID,
			CPU:               provider.GeneralCPU,
			MemoryBytes:       provider.GeneralMemoryBytes,
			RunAsUser:         "1001:1001",
			CapDrop:           []string{"ALL"},
			NoNewPrivileges:   true,
			WorkdirTmpfs:      false,
			CredentialTmpfs:   true,
			HostMounts:        []string{},
			DockerSocket:      false,
			RunnerDownloadURL: provider.RunnerToolURL,
			RunnerFilename:    provider.RunnerToolFilename,
			RunnerSHA256:      provider.RunnerToolSHA256,
			ExecutionProfile:  profile,
		},
		State: mapState(app.State),
	}, nil
}

func composeObject(config map[string]any) (map[string]any, error) {
	if config == nil {
		return nil, errors.New("managed app configuration was not returned")
	}
	if _, ok := config["services"]; ok {
		return config, nil
	}
	for _, key := range []string{"custom_compose_config", "custom_compose_config_string"} {
		value, ok := config[key]
		if !ok {
			continue
		}
		if obj, ok := object(value); ok {
			return obj, nil
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			var obj map[string]any
			if err := json.Unmarshal([]byte(text), &obj); err != nil {
				return nil, fmt.Errorf("parse returned custom Compose JSON: %w", err)
			}
			return obj, nil
		}
	}
	return nil, errors.New("managed app configuration has no recoverable custom Compose payload")
}

func object(value any) (map[string]any, bool) {
	obj, ok := value.(map[string]any)
	return obj, ok
}

func labelMap(value any) (map[string]string, error) {
	out := map[string]string{}
	switch labels := value.(type) {
	case map[string]any:
		for k, v := range labels {
			text, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("label %q is not a string", k)
			}
			out[k] = text
		}
	case map[string]string:
		for k, v := range labels {
			out[k] = v
		}
	case []any:
		for _, raw := range labels {
			text, ok := raw.(string)
			if !ok {
				return nil, errors.New("Compose label list contains a non-string")
			}
			key, value, ok := strings.Cut(text, "=")
			if !ok || key == "" {
				return nil, fmt.Errorf("invalid Compose label %q", text)
			}
			out[key] = value
		}
	default:
		return nil, errors.New("managed app runner service has no readable labels")
	}
	return out, nil
}

func mapState(state string) provider.State {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "RUNNING":
		return provider.StateRunning
	case "STOPPED":
		return provider.StateStopped
	case "CRASHED":
		return provider.StateCrashed
	case "DEPLOYING", "STARTING":
		return provider.StateDeploying
	case "STOPPING":
		return provider.StateStopping
	default:
		// Unknown TrueNAS states are deliberately mapped to an active state so
		// provider.Manager refuses destructive retirement until understood.
		return provider.StateDeploying
	}
}
