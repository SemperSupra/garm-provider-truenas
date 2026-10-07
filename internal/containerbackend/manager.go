package containerbackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/truenasstore"
)

const (
	FlavorLinuxGeneral = "truenas-container-linux-general"
	ImageFamily        = "ubuntu:noble:amd64:default"
)

type Image struct {
	Name    string
	Version string
}

type Container struct {
	ID                 int
	Name               string
	Description        string
	State              string
	Image              Image
	Autostart           bool
	IDMapType           string
	CapabilitiesPolicy string
	InitEnv            map[string]string
}

type CreateSpec struct {
	Name               string
	Description        string
	Image              Image
	Autostart           bool
	IDMapType           string
	CapabilitiesPolicy string
	InitEnv            map[string]string
}

type UpdateSpec struct {
	InitEnv map[string]string
}

type Client interface {
	SystemVersion(context.Context) (string, error)
	ResolveImage(context.Context, string) (Image, error)
	GetByName(context.Context, string) (Container, error)
	Get(context.Context, int) (Container, error)
	List(context.Context) ([]Container, error)
	Create(context.Context, CreateSpec) (Container, error)
	Update(context.Context, int, UpdateSpec) (Container, error)
	Start(context.Context, int) error
	Stop(context.Context, int, bool) error
	Delete(context.Context, int) error
}

type ownership struct {
	Schema       string `json:"schema"`
	ManagedBy    string `json:"managed_by"`
	ControllerID string `json:"controller_id"`
	PoolID       string `json:"pool_id"`
	RunnerName   string `json:"runner_name"`
	Profile      string `json:"profile"`
}

type Manager struct {
	client        Client
	controllerID  string
	systemVersion string
}

func New(client Client, controllerID, systemVersion string) (*Manager, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	if strings.TrimSpace(controllerID) == "" {
		return nil, errors.New("controller ID is required")
	}
	systemVersion = strings.TrimSpace(systemVersion)
	_, cell, err := truenasstore.ResolveRuntimeBackend("container", systemVersion)
	if err != nil {
		return nil, err
	}
	if cell.Driver != "container-v1" || cell.ControlSurface != "container.*" {
		return nil, fmt.Errorf("container-v1 is not the exact driver for %q: %w", systemVersion, provider.ErrUnsupported)
	}
	return &Manager{client: client, controllerID: controllerID, systemVersion: systemVersion}, nil
}

func (m *Manager) Create(ctx context.Context, in provider.Bootstrap) (provider.Instance, error) {
	if err := validateBootstrap(in); err != nil {
		return provider.Instance{}, err
	}
	if err := m.verifySystemVersion(ctx); err != nil {
		return provider.Instance{}, err
	}

	name := ownedName(m.controllerID, in.Name)
	existing, err := m.client.GetByName(ctx, name)
	if err == nil {
		if err := m.verifyOwnership(existing, in.PoolID); err != nil {
			return provider.Instance{}, err
		}
		return toInstance(existing, in.PoolID), nil
	}
	if !errors.Is(err, provider.ErrNotFound) {
		return provider.Instance{}, fmt.Errorf("query existing container: %w", err)
	}

	image, err := m.client.ResolveImage(ctx, ImageFamily)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("resolve exact container image: %w", err)
	}
	if image.Name != ImageFamily || strings.TrimSpace(image.Version) == "" {
		return provider.Instance{}, fmt.Errorf("image family/version did not resolve exactly: %w", provider.ErrManagedDrift)
	}

	desc, err := encodeOwnership(ownership{
		Schema:       "semper-supra.garm-container-owner/1",
		ManagedBy:    "garm-provider-truenas",
		ControllerID: m.controllerID,
		PoolID:       in.PoolID,
		RunnerName:   in.Name,
		Profile:      FlavorLinuxGeneral,
	})
	if err != nil {
		return provider.Instance{}, err
	}

	initialEnv := bootstrapEnv(in)
	created, err := m.client.Create(ctx, CreateSpec{
		Name:               name,
		Description:        desc,
		Image:              image,
		Autostart:           false,
		IDMapType:           "DEFAULT",
		CapabilitiesPolicy: "DEFAULT",
		InitEnv:            initialEnv,
	})
	if err != nil {
		return provider.Instance{}, fmt.Errorf("create container: %w", err)
	}
	if err := m.verifyOwnership(created, in.PoolID); err != nil {
		return provider.Instance{}, err
	}
	if strings.EqualFold(created.State, "RUNNING") {
		return provider.Instance{}, fmt.Errorf("new container unexpectedly started before bootstrap admission: %w", provider.ErrManagedDrift)
	}

	if err := m.client.Start(ctx, created.ID); err != nil {
		return provider.Instance{}, fmt.Errorf("start container: %w", err)
	}

	scrubbed := cloneEnv(initialEnv)
	delete(scrubbed, "GARM_INSTANCE_TOKEN")
	if _, err := m.client.Update(ctx, created.ID, UpdateSpec{InitEnv: scrubbed}); err != nil {
		return provider.Instance{}, fmt.Errorf("scrub persisted bootstrap token: %w", err)
	}

	observed, err := m.client.Get(ctx, created.ID)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("read back scrubbed container: %w", err)
	}
	if err := m.verifyOwnership(observed, in.PoolID); err != nil {
		return provider.Instance{}, err
	}
	if tokenPresent(observed.InitEnv, in.Token) {
		return provider.Instance{}, fmt.Errorf("bootstrap token remains in persisted container desired state: %w", provider.ErrManagedDrift)
	}
	if !strings.EqualFold(observed.State, "RUNNING") {
		return provider.Instance{}, fmt.Errorf("container did not remain running after bootstrap-state scrub: %w", provider.ErrManagedDrift)
	}
	return toInstance(observed, in.PoolID), nil
}

func (m *Manager) Get(ctx context.Context, providerID string) (provider.Instance, error) {
	items, err := m.client.List(ctx)
	if err != nil {
		return provider.Instance{}, err
	}
	for _, item := range items {
		if providerIDFor(item.ID) != providerID {
			continue
		}
		owner, err := m.owner(item)
		if err != nil {
			return provider.Instance{}, err
		}
		return toInstance(item, owner.PoolID), nil
	}
	return provider.Instance{}, provider.ErrNotFound
}

func (m *Manager) List(ctx context.Context, poolID string) ([]provider.Instance, error) {
	items, err := m.client.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Instance, 0, len(items))
	for _, item := range items {
		owner, err := decodeOwnership(item.Description)
		if err != nil || owner.ControllerID != m.controllerID || owner.ManagedBy != "garm-provider-truenas" {
			continue
		}
		if poolID != "" && owner.PoolID != poolID {
			continue
		}
		if err := m.verifyRuntime(item); err != nil {
			return nil, err
		}
		out = append(out, toInstance(item, owner.PoolID))
	}
	return out, nil
}

func (m *Manager) Stop(ctx context.Context, providerID string) error {
	item, _, err := m.findOwned(ctx, providerID)
	if err != nil {
		return err
	}
	if strings.EqualFold(item.State, "STOPPED") {
		return nil
	}
	if !strings.EqualFold(item.State, "RUNNING") {
		return provider.ErrUnsafeOperation
	}
	if err := m.client.Stop(ctx, item.ID, false); err != nil {
		return err
	}
	observed, err := m.client.Get(ctx, item.ID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(observed.State, "STOPPED") {
		return fmt.Errorf("container stop did not reach STOPPED: %w", provider.ErrManagedDrift)
	}
	return nil
}

func (m *Manager) Start(context.Context, string) error {
	return provider.ErrUnsafeOperation
}

func (m *Manager) Delete(ctx context.Context, providerID string) error {
	item, _, err := m.findOwned(ctx, providerID)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(item.State, "STOPPED") {
		return provider.ErrActive
	}
	if err := m.client.Delete(ctx, item.ID); err != nil {
		return err
	}
	if _, err := m.client.Get(ctx, item.ID); !errors.Is(err, provider.ErrNotFound) {
		if err == nil {
			return fmt.Errorf("container still exists after delete: %w", provider.ErrManagedDrift)
		}
		return fmt.Errorf("verify container absence: %w", err)
	}
	return nil
}

func (m *Manager) RemoveAll(ctx context.Context) error {
	items, err := m.List(ctx, "")
	if err != nil {
		return err
	}
	active := false
	for _, item := range items {
		raw, _, err := m.findOwned(ctx, item.ProviderID)
		if err != nil {
			return err
		}
		if !strings.EqualFold(raw.State, "STOPPED") {
			active = true
			continue
		}
		if err := m.Delete(ctx, item.ProviderID); err != nil {
			return err
		}
	}
	if active {
		return provider.ErrActive
	}
	return nil
}

func (m *Manager) verifySystemVersion(ctx context.Context) error {
	got, err := m.client.SystemVersion(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != strings.TrimSpace(m.systemVersion) {
		return fmt.Errorf("exact TrueNAS version mismatch: got %q want %q: %w", got, m.systemVersion, provider.ErrManagedDrift)
	}
	return nil
}

func (m *Manager) findOwned(ctx context.Context, providerID string) (Container, ownership, error) {
	items, err := m.client.List(ctx)
	if err != nil {
		return Container{}, ownership{}, err
	}
	for _, item := range items {
		if providerIDFor(item.ID) != providerID {
			continue
		}
		owner, err := m.owner(item)
		return item, owner, err
	}
	return Container{}, ownership{}, provider.ErrNotFound
}

func (m *Manager) verifyOwnership(item Container, poolID string) error {
	owner, err := m.owner(item)
	if err != nil {
		return err
	}
	if poolID != "" && owner.PoolID != poolID {
		return fmt.Errorf("pool mismatch: %w", provider.ErrForeign)
	}
	return nil
}

func (m *Manager) owner(item Container) (ownership, error) {
	owner, err := decodeOwnership(item.Description)
	if err != nil {
		return ownership{}, provider.ErrForeign
	}
	if owner.Schema != "semper-supra.garm-container-owner/1" ||
		owner.ManagedBy != "garm-provider-truenas" ||
		owner.ControllerID != m.controllerID ||
		owner.Profile != FlavorLinuxGeneral {
		return ownership{}, provider.ErrForeign
	}
	if err := m.verifyRuntime(item); err != nil {
		return ownership{}, err
	}
	return owner, nil
}

func (m *Manager) verifyRuntime(item Container) error {
	if item.Autostart || item.IDMapType != "DEFAULT" || item.CapabilitiesPolicy != "DEFAULT" {
		return provider.ErrManagedDrift
	}
	if item.Image.Name != ImageFamily || strings.TrimSpace(item.Image.Version) == "" {
		return provider.ErrManagedDrift
	}
	switch strings.ToUpper(item.State) {
	case "RUNNING", "STOPPED":
		return nil
	default:
		return provider.ErrManagedDrift
	}
}

func validateBootstrap(in provider.Bootstrap) error {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.PoolID) == "" {
		return provider.ErrUnsupported
	}
	if in.OSType != "linux" || (in.Arch != "amd64" && in.Arch != "x64" && in.Arch != "x86_64") {
		return provider.ErrUnsupported
	}
	if in.Flavor != FlavorLinuxGeneral {
		return provider.ErrUnsupported
	}
	if strings.TrimSpace(in.CallbackURL) == "" || strings.TrimSpace(in.MetadataURL) == "" || strings.TrimSpace(in.Token) == "" {
		return provider.ErrUnsupported
	}
	return nil
}

func bootstrapEnv(in provider.Bootstrap) map[string]string {
	return map[string]string{
		"GARM_CALLBACK_URL":  in.CallbackURL,
		"GARM_METADATA_URL":  in.MetadataURL,
		"GARM_INSTANCE_TOKEN": in.Token,
		"GARM_RUNNER_DOWNLOAD_URL": provider.RunnerToolURL,
		"GARM_RUNNER_FILENAME":     provider.RunnerToolFilename,
		"GARM_RUNNER_SHA256":       provider.RunnerToolSHA256,
	}
}

func tokenPresent(env map[string]string, token string) bool {
	for key, value := range env {
		if key == "GARM_INSTANCE_TOKEN" || (token != "" && strings.Contains(value, token)) {
			return true
		}
	}
	return false
}

func cloneEnv(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func encodeOwnership(owner ownership) (string, error) {
	raw, err := json.Marshal(owner)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeOwnership(raw string) (ownership, error) {
	var owner ownership
	if err := json.Unmarshal([]byte(raw), &owner); err != nil {
		return owner, err
	}
	return owner, nil
}

func ownedName(controllerID, requested string) string {
	raw := sanitize(controllerID) + "-" + sanitize(requested)
	sum := sha256.Sum256([]byte(raw))
	prefix := strings.Trim("garm-"+raw, "-")
	if len(prefix) > 48 {
		prefix = strings.Trim(prefix[:48], "-")
	}
	return prefix + "-" + hex.EncodeToString(sum[:4])
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

func providerIDFor(id int) string {
	return fmt.Sprintf("container:%d", id)
}

func toInstance(item Container, poolID string) provider.Instance {
	return provider.Instance{
		ProviderID: providerIDFor(item.ID),
		Name:       item.Name,
		OSType:     "linux",
		OSName:     "truenas-system-container",
		OSVersion:  item.Image.Version,
		OSArch:     "x86_64",
		Status:     strings.ToLower(item.State),
		PoolID:     poolID,
	}
}
