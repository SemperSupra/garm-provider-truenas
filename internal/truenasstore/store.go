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
	labelManaged         = "io.sempersupra.garm.managed"
	labelSchema          = "io.sempersupra.garm.schema"
	labelController      = "io.sempersupra.garm.controller-id"
	labelPool            = "io.sempersupra.garm.pool-id"
	labelProfile         = "io.sempersupra.garm.execution-profile"
	metadataSchema       = "1"
	credentialTmpfsMount = "/run/garm-jit:rw,nosuid,nodev,noexec,uid=1001,gid=1001,mode=0700"
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
					credentialTmpfsMount,
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

func managedDriftf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), provider.ErrManagedDrift)
}

func integerValue(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		if uint64(n) > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case float64:
		i := int64(n)
		return i, float64(i) == n
	case float32:
		i := int64(n)
		return i, float32(i) == n
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func validateFixedRunnerService(runner map[string]any, labels map[string]string) (map[string]string, error) {
	requiredKeys := map[string]bool{
		"image": true, "user": true, "restart": true, "entrypoint": true,
		"cap_drop": true, "security_opt": true, "cpus": true, "mem_limit": true,
		"tmpfs": true, "labels": true, "environment": true,
	}
	allowedKeys := map[string]bool{}
	for key := range requiredKeys {
		allowedKeys[key] = true
	}
	allowedKeys["extra_hosts"] = true
	for key := range runner {
		if !allowedKeys[key] {
			return nil, managedDriftf("runner service contains unexpected field %q", key)
		}
	}
	for key := range requiredKeys {
		if _, ok := runner[key]; !ok {
			return nil, managedDriftf("runner service is missing required field %q", key)
		}
	}

	image, ok := runner["image"].(string)
	if !ok || image != provider.RunnerImage {
		return nil, managedDriftf("runner image drifted")
	}
	user, ok := runner["user"].(string)
	if !ok || user != "1001:1001" {
		return nil, managedDriftf("runner user drifted")
	}
	restart, ok := runner["restart"].(string)
	if !ok || restart != "no" {
		return nil, managedDriftf("runner restart policy drifted")
	}

	entrypoint, err := stringList(runner["entrypoint"])
	if err != nil || len(entrypoint) != 3 || entrypoint[0] != "/bin/sh" || entrypoint[1] != "-c" || entrypoint[2] != containerBootstrapCommand() {
		return nil, managedDriftf("runner bootstrap entrypoint drifted")
	}
	caps, err := stringList(runner["cap_drop"])
	if err != nil || len(caps) != 1 || caps[0] != "ALL" {
		return nil, managedDriftf("runner capability-drop profile drifted")
	}
	securityOpts, err := stringList(runner["security_opt"])
	if err != nil || len(securityOpts) != 1 || securityOpts[0] != "no-new-privileges:true" {
		return nil, managedDriftf("runner security options drifted")
	}
	cpu, ok := integerValue(runner["cpus"])
	if !ok || cpu != int64(provider.GeneralCPU) {
		return nil, managedDriftf("runner CPU limit drifted")
	}
	memory, ok := integerValue(runner["mem_limit"])
	if !ok || memory != provider.GeneralMemoryBytes {
		return nil, managedDriftf("runner memory limit drifted")
	}
	tmpfs, err := stringList(runner["tmpfs"])
	if err != nil || len(tmpfs) != 1 || tmpfs[0] != credentialTmpfsMount {
		return nil, managedDriftf("runner credential tmpfs drifted")
	}

	allowedLabels := map[string]bool{
		labelManaged:             true,
		labelSchema:              true,
		labelController:          true,
		labelPool:                true,
		labelProfile:             true,
		labelCallbackHostGateway: true,
	}
	for key := range labels {
		if !allowedLabels[key] {
			return nil, managedDriftf("runner carries unexpected provider label %q", key)
		}
	}
	if value, present := labels[labelCallbackHostGateway]; present && value != "true" {
		return nil, managedDriftf("callback host-gateway ownership label drifted")
	}
	_, extraHostsPresent := runner["extra_hosts"]
	_, gatewayLabelPresent := labels[labelCallbackHostGateway]
	if extraHostsPresent != gatewayLabelPresent {
		return nil, managedDriftf("callback host-gateway field/label presence drifted")
	}

	environment, err := stringMap(runner["environment"])
	if err != nil {
		return nil, managedDriftf("runner environment is unreadable")
	}
	requiredEnvironment := map[string]bool{
		"GARM_CALLBACK_URL":        true,
		"GARM_METADATA_URL":        true,
		"GARM_INSTANCE_TOKEN":      true,
		"GARM_RUNNER_DOWNLOAD_URL": true,
		"GARM_RUNNER_FILENAME":     true,
		"GARM_RUNNER_SHA256":       true,
	}
	if len(environment) != len(requiredEnvironment) {
		return nil, managedDriftf("runner environment field count drifted")
	}
	for key := range requiredEnvironment {
		if _, ok := environment[key]; !ok {
			return nil, managedDriftf("runner environment is missing %s", key)
		}
	}
	if strings.TrimSpace(environment["GARM_CALLBACK_URL"]) == "" ||
		strings.TrimSpace(environment["GARM_METADATA_URL"]) == "" ||
		strings.TrimSpace(environment["GARM_INSTANCE_TOKEN"]) == "" {
		return nil, managedDriftf("runner bootstrap callback/metadata/token contract drifted")
	}
	if environment["GARM_RUNNER_DOWNLOAD_URL"] != provider.RunnerToolURL ||
		environment["GARM_RUNNER_FILENAME"] != provider.RunnerToolFilename ||
		environment["GARM_RUNNER_SHA256"] != provider.RunnerToolSHA256 {
		return nil, managedDriftf("runner payload metadata drifted")
	}

	return environment, nil
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
	if len(services) != 1 {
		return provider.App{}, managedDriftf("managed app contains unexpected services")
	}
	if labels[labelSchema] != metadataSchema {
		return provider.App{}, managedDriftf("unsupported ownership metadata schema %q", labels[labelSchema])
	}
	controllerID := strings.TrimSpace(labels[labelController])
	poolID := strings.TrimSpace(labels[labelPool])
	profile := strings.TrimSpace(labels[labelProfile])
	if controllerID == "" || poolID == "" || profile == "" {
		return provider.App{}, managedDriftf("managed app ownership metadata is incomplete")
	}
	if profile != provider.FlavorLinuxGeneral {
		return provider.App{}, managedDriftf("managed app execution profile drifted")
	}

	environment, err := validateFixedRunnerService(runner, labels)
	if err != nil {
		return provider.App{}, err
	}

	return provider.App{
		Spec: provider.AppSpec{
			Name:              app.Name,
			Image:             provider.RunnerImage,
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
			CallbackURL:       environment["GARM_CALLBACK_URL"],
			MetadataURL:       environment["GARM_METADATA_URL"],
			RunnerDownloadURL: environment["GARM_RUNNER_DOWNLOAD_URL"],
			RunnerFilename:    environment["GARM_RUNNER_FILENAME"],
			RunnerSHA256:      environment["GARM_RUNNER_SHA256"],
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
