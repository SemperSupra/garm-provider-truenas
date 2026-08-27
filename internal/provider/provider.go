package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound        = errors.New("instance not found")
	ErrActive          = errors.New("instance is active; refusing destructive operation")
	ErrForeign         = errors.New("instance is not owned by this controller")
	ErrUnsupported     = errors.New("unsupported execution request")
	ErrUnsafeOperation = errors.New("operation is unsafe for one-job ephemeral runners")
)

const (
	FlavorLinuxGeneral = "truenas-linux-general"
	RunnerImage        = "ghcr.io/actions/actions-runner:2.336.0@sha256:0cfdcc701ce933c6d243c6b0b2da767366dc9f2e99961d4c3754b0b78084cdda"
	RunnerVersion      = "2.336.0"
	RunnerToolFilename = "actions-runner-linux-x64-" + RunnerVersion + ".tar.gz"
	RunnerToolURL      = "https://github.com/actions/runner/releases/download/v" + RunnerVersion + "/" + RunnerToolFilename
	RunnerToolSHA256   = "04cf0be1aff4c3ec3554466c39124ca250e3effd8873bb7e8d68535aa9505d5d"
	GeneralCPU         = 4
	GeneralMemoryBytes = int64(8 * 1024 * 1024 * 1024)
	TrueNASAppNameMax  = 40
)

type State string

const (
	StateDeploying State = "DEPLOYING"
	StateRunning   State = "RUNNING"
	StateStopping  State = "STOPPING"
	StateStopped   State = "STOPPED"
	StateCrashed   State = "CRASHED"
)

type Bootstrap struct {
	Name        string `json:"name"`
	OSType      string `json:"os_type"`
	Arch        string `json:"arch"`
	Flavor      string `json:"flavor"`
	PoolID      string `json:"pool_id"`
	CallbackURL string `json:"callback-url"`
	MetadataURL string `json:"metadata-url"`
	Token       string `json:"instance-token"`
}

type AppSpec struct {
	Name              string   `json:"name"`
	Image             string   `json:"image"`
	ControllerID      string   `json:"controller_id"`
	PoolID            string   `json:"pool_id"`
	CPU               int      `json:"cpu"`
	MemoryBytes       int64    `json:"memory_bytes"`
	RunAsUser         string   `json:"run_as_user"`
	CapDrop           []string `json:"cap_drop"`
	NoNewPrivileges   bool     `json:"no_new_privileges"`
	WorkdirTmpfs      bool     `json:"workdir_tmpfs"`
	CredentialTmpfs   bool     `json:"credential_tmpfs"`
	HostMounts        []string `json:"host_mounts"`
	DockerSocket      bool     `json:"docker_socket"`
	CallbackURL       string   `json:"callback_url"`
	MetadataURL       string   `json:"metadata_url"`
	BootstrapToken    string   `json:"bootstrap_token"`
	RunnerDownloadURL string   `json:"runner_download_url"`
	RunnerFilename    string   `json:"runner_filename"`
	RunnerSHA256      string   `json:"runner_sha256"`
	ExecutionProfile  string   `json:"execution_profile"`
}

type App struct {
	Spec  AppSpec `json:"spec"`
	State State   `json:"state"`
}

type Instance struct {
	ProviderID    string `json:"provider_id"`
	Name          string `json:"name"`
	OSType        string `json:"os_type"`
	OSName        string `json:"os_name"`
	OSVersion     string `json:"os_version"`
	OSArch        string `json:"os_arch"`
	Status        string `json:"status"`
	PoolID        string `json:"pool_id"`
	ProviderFault string `json:"provider_fault"`
}

type Client interface {
	CreateApp(context.Context, AppSpec) (App, error)
	GetApp(context.Context, string) (App, error)
	ListApps(context.Context) ([]App, error)
	DeleteApp(context.Context, string) error
}

type Manager struct {
	client       Client
	controllerID string
}

func NewManager(client Client, controllerID string) (*Manager, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	if strings.TrimSpace(controllerID) == "" || sanitize(controllerID) == "" {
		return nil, errors.New("controller ID is required and must contain an alphanumeric character")
	}
	return &Manager{client: client, controllerID: controllerID}, nil
}

func (m *Manager) Create(ctx context.Context, in Bootstrap) (Instance, error) {
	if err := validateBootstrap(in); err != nil {
		return Instance{}, err
	}

	name := ownedName(m.controllerID, in.Name)
	existing, err := m.client.GetApp(ctx, name)
	if err == nil {
		if err := m.verifyOwnership(existing, in.PoolID); err != nil {
			return Instance{}, err
		}
		return toInstance(existing), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Instance{}, fmt.Errorf("query existing app: %w", err)
	}

	spec := AppSpec{
		Name:              name,
		Image:             RunnerImage,
		ControllerID:      m.controllerID,
		PoolID:            in.PoolID,
		CPU:               GeneralCPU,
		MemoryBytes:       GeneralMemoryBytes,
		RunAsUser:         "1001:1001",
		CapDrop:           []string{"ALL"},
		NoNewPrivileges:   true,
		WorkdirTmpfs:      false,
		CredentialTmpfs:   true,
		HostMounts:        []string{},
		DockerSocket:      false,
		CallbackURL:       in.CallbackURL,
		MetadataURL:       in.MetadataURL,
		BootstrapToken:    in.Token,
		RunnerDownloadURL: RunnerToolURL,
		RunnerFilename:    RunnerToolFilename,
		RunnerSHA256:      RunnerToolSHA256,
		ExecutionProfile:  FlavorLinuxGeneral,
	}

	created, err := m.client.CreateApp(ctx, spec)
	if err != nil {
		return Instance{}, fmt.Errorf("create app: %w", err)
	}
	if err := m.verifyOwnership(created, in.PoolID); err != nil {
		return Instance{}, err
	}
	return toInstance(created), nil
}

func (m *Manager) Get(ctx context.Context, providerID string) (Instance, error) {
	app, err := m.client.GetApp(ctx, providerID)
	if err != nil {
		return Instance{}, err
	}
	if err := m.verifyOwnership(app, ""); err != nil {
		return Instance{}, err
	}
	return toInstance(app), nil
}

func (m *Manager) List(ctx context.Context, poolID string) ([]Instance, error) {
	apps, err := m.client.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(apps))
	for _, app := range apps {
		if app.Spec.ControllerID != m.controllerID {
			continue
		}
		if poolID != "" && app.Spec.PoolID != poolID {
			continue
		}
		out = append(out, toInstance(app))
	}
	return out, nil
}

func (m *Manager) Delete(ctx context.Context, providerID string) error {
	app, err := m.client.GetApp(ctx, providerID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.verifyOwnership(app, ""); err != nil {
		return err
	}
	if isActive(app.State) {
		return ErrActive
	}
	return m.client.DeleteApp(ctx, providerID)
}

func (m *Manager) Start(context.Context, string) error {
	return ErrUnsafeOperation
}

func (m *Manager) Stop(ctx context.Context, providerID string) error {
	app, err := m.client.GetApp(ctx, providerID)
	if err != nil {
		return err
	}
	if err := m.verifyOwnership(app, ""); err != nil {
		return err
	}
	if app.State == StateStopped {
		return nil
	}
	return ErrUnsafeOperation
}

func (m *Manager) RemoveAll(ctx context.Context) error {
	apps, err := m.client.ListApps(ctx)
	if err != nil {
		return err
	}
	active := false
	for _, app := range apps {
		if app.Spec.ControllerID != m.controllerID {
			continue
		}
		if isActive(app.State) {
			active = true
			continue
		}
		if err := m.client.DeleteApp(ctx, app.Spec.Name); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if active {
		return ErrActive
	}
	return nil
}

func (m *Manager) verifyOwnership(app App, poolID string) error {
	if app.Spec.ControllerID != m.controllerID {
		return ErrForeign
	}
	if poolID != "" && app.Spec.PoolID != poolID {
		return fmt.Errorf("pool mismatch: %w", ErrForeign)
	}
	return nil
}

func validateBootstrap(in Bootstrap) error {
	if strings.TrimSpace(in.Name) == "" || sanitize(in.Name) == "" || strings.TrimSpace(in.PoolID) == "" {
		return fmt.Errorf("name and pool ID are required and name must contain an alphanumeric character: %w", ErrUnsupported)
	}
	if in.OSType != "linux" {
		return fmt.Errorf("os_type %q: %w", in.OSType, ErrUnsupported)
	}
	if in.Arch != "amd64" && in.Arch != "x64" && in.Arch != "x86_64" {
		return fmt.Errorf("arch %q: %w", in.Arch, ErrUnsupported)
	}
	if in.Flavor != FlavorLinuxGeneral {
		return fmt.Errorf("flavor %q: %w", in.Flavor, ErrUnsupported)
	}
	if strings.TrimSpace(in.CallbackURL) == "" || strings.TrimSpace(in.MetadataURL) == "" || strings.TrimSpace(in.Token) == "" {
		return fmt.Errorf("bootstrap callback, metadata URL and token are required: %w", ErrUnsupported)
	}
	return nil
}

func ownedName(controllerID, requested string) string {
	controller := sanitize(controllerID)
	requested = sanitize(requested)
	if len(controller) > 12 {
		controller = controller[:12]
	}
	raw := strings.Trim("garm-"+controller+"-"+requested, "-")
	if len(raw) <= TrueNASAppNameMax {
		return raw
	}

	// TrueNAS 25.04 app names are capped at 40 characters. Preserve a readable
	// prefix and append a stable digest so simple truncation cannot alias two
	// distinct long GARM runner names.
	digest := sha256.Sum256([]byte(raw))
	suffix := hex.EncodeToString(digest[:4])
	prefixLen := TrueNASAppNameMax - len(suffix) - 1
	prefix := strings.Trim(raw[:prefixLen], "-")
	return prefix + "-" + suffix
}

func sanitize(in string) string {
	in = strings.ToLower(in)
	var b strings.Builder
	lastDash := false
	for _, r := range in {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func isActive(state State) bool {
	switch state {
	case StateDeploying, StateRunning, StateStopping:
		return true
	default:
		return false
	}
}

func toInstance(app App) Instance {
	return Instance{
		ProviderID: app.Spec.Name,
		Name:       app.Spec.Name,
		OSType:     "linux",
		OSName:     "truenas-custom-app",
		OSVersion:  "mvp",
		OSArch:     "x86_64",
		Status:     strings.ToLower(string(app.State)),
		PoolID:     app.Spec.PoolID,
	}
}
