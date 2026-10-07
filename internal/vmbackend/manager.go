package vmbackend

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
	FlavorLinuxGeneral = "truenas-vm-linux-general"
	TemplateFamily     = "ubuntu-24.04-amd64-template"
)

type Template struct {
	ID      int
	Name    string
	Version string
}

type VM struct {
	ID          int
	Name        string
	Description string
	State       string
	Autostart   bool
	VCPUs       int
	MemoryBytes int64
	Template    Template
	SeedRef     string
}

type CloneSpec struct {
	Name        string
	Description string
	VCPUs       int
	MemoryBytes int64
	Autostart   bool
}

type SeedSpec struct {
	MetaData string
	UserData string
}

type Client interface {
	SystemVersion(context.Context) (string, error)
	ResolveTemplate(context.Context, string) (Template, error)
	GetByName(context.Context, string) (VM, error)
	Get(context.Context, int) (VM, error)
	List(context.Context) ([]VM, error)
	Clone(context.Context, int, CloneSpec) (VM, error)
	StageSeed(context.Context, int, SeedSpec) (string, error)
	AttachSeed(context.Context, int, string) error
	DetachSeed(context.Context, int, string) error
	DeleteSeed(context.Context, string) error
	BootstrapConsumed(context.Context, int) (bool, error)
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
	_, cell, err := truenasstore.ResolveRuntimeBackend("vm", systemVersion)
	if err != nil {
		return nil, err
	}
	if cell.Status == "NOT_ADMITTED" {
		return nil, provider.ErrUnsupported
	}
	if cell.Driver != "vm-v1" || cell.ControlSurface != "vm.*" {
		return nil, fmt.Errorf("vm-v1 is not the exact driver for %q: %w", systemVersion, provider.ErrUnsupported)
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
		return provider.Instance{}, fmt.Errorf("query existing VM: %w", err)
	}

	template, err := m.client.ResolveTemplate(ctx, TemplateFamily)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("resolve exact VM template: %w", err)
	}
	if template.Name != TemplateFamily || template.ID <= 0 || strings.TrimSpace(template.Version) == "" {
		return provider.Instance{}, fmt.Errorf("template family/version did not resolve exactly: %w", provider.ErrManagedDrift)
	}

	desc, err := encodeOwnership(ownership{
		Schema:       "semper-supra.garm-vm-owner/1",
		ManagedBy:    "garm-provider-truenas",
		ControllerID: m.controllerID,
		PoolID:       in.PoolID,
		RunnerName:   in.Name,
		Profile:      FlavorLinuxGeneral,
	})
	if err != nil {
		return provider.Instance{}, err
	}

	created, err := m.client.Clone(ctx, template.ID, CloneSpec{
		Name:        name,
		Description: desc,
		VCPUs:       provider.GeneralCPU,
		MemoryBytes: provider.GeneralMemoryBytes,
		Autostart:   false,
	})
	if err != nil {
		return provider.Instance{}, fmt.Errorf("clone VM template: %w", err)
	}
	if err := m.verifyOwnership(created, in.PoolID); err != nil {
		return provider.Instance{}, err
	}
	if !strings.EqualFold(created.State, "STOPPED") {
		return provider.Instance{}, fmt.Errorf("new VM unexpectedly active before bootstrap admission: %w", provider.ErrManagedDrift)
	}

	seedRef, err := m.client.StageSeed(ctx, created.ID, seedFor(in, name))
	if err != nil {
		return provider.Instance{}, fmt.Errorf("stage NoCloud seed: %w", err)
	}
	if strings.TrimSpace(seedRef) == "" || strings.Contains(seedRef, in.Token) {
		return provider.Instance{}, fmt.Errorf("unsafe NoCloud seed reference: %w", provider.ErrManagedDrift)
	}
	if err := m.client.AttachSeed(ctx, created.ID, seedRef); err != nil {
		return provider.Instance{}, fmt.Errorf("attach NoCloud seed: %w", err)
	}
	if err := m.client.Start(ctx, created.ID); err != nil {
		return provider.Instance{}, fmt.Errorf("start VM: %w", err)
	}

	observed, err := m.client.Get(ctx, created.ID)
	if err != nil {
		return provider.Instance{}, fmt.Errorf("read back started VM: %w", err)
	}
	if err := m.verifyOwnership(observed, in.PoolID); err != nil {
		return provider.Instance{}, err
	}
	if !strings.EqualFold(observed.State, "RUNNING") {
		return provider.Instance{}, fmt.Errorf("VM did not reach RUNNING: %w", provider.ErrManagedDrift)
	}
	if observed.SeedRef != seedRef {
		return provider.Instance{}, fmt.Errorf("NoCloud seed attachment drift: %w", provider.ErrManagedDrift)
	}
	return toInstance(observed, in.PoolID), nil
}

// ReconcileBootstrap is deliberately separate from Create. A one-job runner may
// not remove its bootstrap seed until an independent consumption signal exists.
// The concrete signal and middleware lowering remain qualification gates.
func (m *Manager) ReconcileBootstrap(ctx context.Context, providerID string) error {
	item, _, err := m.findOwned(ctx, providerID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(item.SeedRef) == "" {
		return nil
	}
	// TrueNAS vm.device.delete refuses device removal while the VM is active.
	// Do not model an early live detach that the supported middleware cannot
	// realize. Seed cleanup is therefore a stopped-state operation and is
	// normally completed as part of runner retirement.
	if !strings.EqualFold(item.State, "STOPPED") {
		return provider.ErrUnsafeOperation
	}
	consumed, err := m.client.BootstrapConsumed(ctx, item.ID)
	if err != nil {
		return fmt.Errorf("observe bootstrap consumption: %w", err)
	}
	if !consumed {
		return provider.ErrUnsafeOperation
	}
	seedRef := item.SeedRef
	if err := m.client.DetachSeed(ctx, item.ID, seedRef); err != nil {
		return fmt.Errorf("detach bootstrap seed: %w", err)
	}
	if err := m.client.DeleteSeed(ctx, seedRef); err != nil {
		return fmt.Errorf("delete bootstrap seed: %w", err)
	}
	observed, err := m.client.Get(ctx, item.ID)
	if err != nil {
		return fmt.Errorf("verify bootstrap seed cleanup: %w", err)
	}
	if strings.TrimSpace(observed.SeedRef) != "" {
		return fmt.Errorf("bootstrap seed remains attached: %w", provider.ErrManagedDrift)
	}
	return nil
}

func (m *Manager) Get(ctx context.Context, providerID string) (provider.Instance, error) {
	item, owner, err := m.findOwned(ctx, providerID)
	if err != nil {
		return provider.Instance{}, err
	}
	return toInstance(item, owner.PoolID), nil
}

func (m *Manager) List(ctx context.Context, poolID string) ([]provider.Instance, error) {
	items, err := m.client.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Instance, 0, len(items))
	for _, item := range items {
		owner, err := decodeOwnership(item.Description)
		if err != nil || owner.ManagedBy != "garm-provider-truenas" || owner.ControllerID != m.controllerID {
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
		return fmt.Errorf("VM stop did not reach STOPPED: %w", provider.ErrManagedDrift)
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
	if strings.TrimSpace(item.SeedRef) != "" {
		seedRef := item.SeedRef
		if err := m.client.DetachSeed(ctx, item.ID, seedRef); err != nil {
			return fmt.Errorf("detach seed before VM delete: %w", err)
		}
		if err := m.client.DeleteSeed(ctx, seedRef); err != nil {
			return fmt.Errorf("delete seed before VM delete: %w", err)
		}
	}
	if err := m.client.Delete(ctx, item.ID); err != nil {
		return err
	}
	if _, err := m.client.Get(ctx, item.ID); !errors.Is(err, provider.ErrNotFound) {
		if err == nil {
			return fmt.Errorf("VM still exists after delete: %w", provider.ErrManagedDrift)
		}
		return fmt.Errorf("verify VM absence: %w", err)
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

func (m *Manager) findOwned(ctx context.Context, providerID string) (VM, ownership, error) {
	items, err := m.client.List(ctx)
	if err != nil {
		return VM{}, ownership{}, err
	}
	for _, item := range items {
		if providerIDFor(item.ID) != providerID {
			continue
		}
		owner, err := m.owner(item)
		return item, owner, err
	}
	return VM{}, ownership{}, provider.ErrNotFound
}

func (m *Manager) verifyOwnership(item VM, poolID string) error {
	owner, err := m.owner(item)
	if err != nil {
		return err
	}
	if poolID != "" && owner.PoolID != poolID {
		return fmt.Errorf("pool mismatch: %w", provider.ErrForeign)
	}
	return nil
}

func (m *Manager) owner(item VM) (ownership, error) {
	owner, err := decodeOwnership(item.Description)
	if err != nil {
		return ownership{}, provider.ErrForeign
	}
	if owner.Schema != "semper-supra.garm-vm-owner/1" ||
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

func (m *Manager) verifyRuntime(item VM) error {
	if item.Autostart || item.VCPUs != provider.GeneralCPU || item.MemoryBytes != provider.GeneralMemoryBytes {
		return provider.ErrManagedDrift
	}
	if item.Template.Name != TemplateFamily || item.Template.ID <= 0 || strings.TrimSpace(item.Template.Version) == "" {
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

func seedFor(in provider.Bootstrap, hostname string) SeedSpec {
	meta := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", hostname, hostname)
	user := fmt.Sprintf(
		"#cloud-config\n"+
			"write_files:\n"+
			"  - path: /run/garm/bootstrap.env\n"+
			"    permissions: '0600'\n"+
			"    content: |\n"+
			"      GARM_CALLBACK_URL=%s\n"+
			"      GARM_METADATA_URL=%s\n"+
			"      GARM_INSTANCE_TOKEN=%s\n"+
			"      GARM_RUNNER_DOWNLOAD_URL=%s\n"+
			"      GARM_RUNNER_FILENAME=%s\n"+
			"      GARM_RUNNER_SHA256=%s\n"+
			"runcmd:\n"+
			"  - [\"/usr/local/libexec/garm-bootstrap\", \"/run/garm/bootstrap.env\"]\n",
		in.CallbackURL,
		in.MetadataURL,
		in.Token,
		provider.RunnerToolURL,
		provider.RunnerToolFilename,
		provider.RunnerToolSHA256,
	)
	return SeedSpec{MetaData: meta, UserData: user}
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
	return fmt.Sprintf("vm:%d", id)
}

func toInstance(item VM, poolID string) provider.Instance {
	return provider.Instance{
		ProviderID: providerIDFor(item.ID),
		Name:       item.Name,
		OSType:     "linux",
		OSName:     "truenas-vm",
		OSVersion:  item.Template.Version,
		OSArch:     "x86_64",
		Status:     strings.ToLower(item.State),
		PoolID:     poolID,
	}
}
