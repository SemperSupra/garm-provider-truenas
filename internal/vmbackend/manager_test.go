package vmbackend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type fakeClient struct {
	version  string
	template Template
	items    map[int]VM
	seeds    map[string]SeedSpec
	nextID   int
	consumed map[int]bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		version:  "TrueNAS-26.0.0-BETA.3",
		template: Template{ID: 42, Name: TemplateFamily, Version: "ubuntu-24.04.3"},
		items:    map[int]VM{},
		seeds:    map[string]SeedSpec{},
		nextID:   1,
		consumed: map[int]bool{},
	}
}

func (f *fakeClient) SystemVersion(context.Context) (string, error) { return f.version, nil }
func (f *fakeClient) ResolveTemplate(context.Context, string) (Template, error) {
	return f.template, nil
}
func (f *fakeClient) GetByName(_ context.Context, name string) (VM, error) {
	for _, item := range f.items {
		if item.Name == name {
			return item, nil
		}
	}
	return VM{}, provider.ErrNotFound
}
func (f *fakeClient) Get(_ context.Context, id int) (VM, error) {
	item, ok := f.items[id]
	if !ok {
		return VM{}, provider.ErrNotFound
	}
	return item, nil
}
func (f *fakeClient) List(context.Context) ([]VM, error) {
	out := make([]VM, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out, nil
}
func (f *fakeClient) Clone(_ context.Context, templateID int, spec CloneSpec) (VM, error) {
	if templateID != f.template.ID {
		return VM{}, provider.ErrManagedDrift
	}
	id := f.nextID
	f.nextID++
	item := VM{
		ID: id, Name: spec.Name, Description: spec.Description, State: "STOPPED",
		Autostart: spec.Autostart, VCPUs: spec.VCPUs, MemoryBytes: spec.MemoryBytes,
		Template: f.template,
	}
	f.items[id] = item
	return item, nil
}
func (f *fakeClient) StageSeed(_ context.Context, id int, seed SeedSpec) (string, error) {
	if _, ok := f.items[id]; !ok {
		return "", provider.ErrNotFound
	}
	ref := "/mnt/garm-seeds/seed-" + providerIDFor(id) + ".iso"
	f.seeds[ref] = seed
	return ref, nil
}
func (f *fakeClient) AttachSeed(_ context.Context, id int, ref string) error {
	item, ok := f.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	if _, ok := f.seeds[ref]; !ok {
		return provider.ErrNotFound
	}
	item.SeedRef = ref
	f.items[id] = item
	return nil
}
func (f *fakeClient) DetachSeed(_ context.Context, id int, ref string) error {
	item, ok := f.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	if item.SeedRef != ref {
		return provider.ErrManagedDrift
	}
	item.SeedRef = ""
	f.items[id] = item
	return nil
}
func (f *fakeClient) DeleteSeed(_ context.Context, ref string) error {
	delete(f.seeds, ref)
	return nil
}
func (f *fakeClient) BootstrapConsumed(_ context.Context, id int) (bool, error) {
	return f.consumed[id], nil
}
func (f *fakeClient) Start(_ context.Context, id int) error {
	item, ok := f.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	item.State = "RUNNING"
	f.items[id] = item
	return nil
}
func (f *fakeClient) Stop(_ context.Context, id int, _ bool) error {
	item, ok := f.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	item.State = "STOPPED"
	f.items[id] = item
	return nil
}
func (f *fakeClient) Delete(_ context.Context, id int) error {
	delete(f.items, id)
	return nil
}

func bootstrap() provider.Bootstrap {
	return provider.Bootstrap{
		Name: "runner-1", OSType: "linux", Arch: "amd64", Flavor: FlavorLinuxGeneral,
		PoolID: "pool-1", CallbackURL: "https://garm.example/callback",
		MetadataURL: "https://garm.example/metadata", Token: "secret-bootstrap-token",
	}
}

func TestCreateClonesStoppedTemplateAndStagesSeed(t *testing.T) {
	client := newFakeClient()
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manager.Create(context.Background(), bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	raw := client.items[1]
	if raw.State != "RUNNING" {
		t.Fatalf("expected RUNNING after first boot, got %q", raw.State)
	}
	if raw.Autostart || raw.VCPUs != provider.GeneralCPU || raw.MemoryBytes != provider.GeneralMemoryBytes {
		t.Fatalf("VM profile drift: %#v", raw)
	}
	if got.ProviderID != "vm:1" || got.PoolID != "pool-1" {
		t.Fatalf("unexpected provider instance: %#v", got)
	}
	if strings.Contains(raw.Description, bootstrap().Token) {
		t.Fatal("bootstrap token leaked into ownership metadata")
	}
	seed, ok := client.seeds[raw.SeedRef]
	if !ok || !strings.Contains(seed.UserData, bootstrap().Token) {
		t.Fatal("candidate NoCloud seed did not carry the bootstrap token")
	}
}

func TestBootstrapSeedCleanupRequiresIndependentConsumptionSignal(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	inst, err := manager.Create(context.Background(), bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBootstrap(context.Background(), inst.ProviderID); !errors.Is(err, provider.ErrUnsafeOperation) {
		t.Fatalf("expected fail-closed cleanup before consumption, got %v", err)
	}
	client.consumed[1] = true
	if err := manager.ReconcileBootstrap(context.Background(), inst.ProviderID); err != nil {
		t.Fatal(err)
	}
	if client.items[1].SeedRef != "" || len(client.seeds) != 0 {
		t.Fatalf("bootstrap seed residue remains: vm=%#v seeds=%#v", client.items[1], client.seeds)
	}
}

func TestExactVersionMismatchFailsClosed(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	client.version = "TrueNAS-26.0.0-BETA.4"
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected exact-version mismatch, got %v", err)
	}
}

func Test25041VMRemainsNotAdmitted(t *testing.T) {
	client := newFakeClient()
	client.version = "TrueNAS-25.04.1"
	if _, err := New(client, "controller-1", client.version); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("expected 25.04.1 VM to remain not admitted, got %v", err)
	}
}

func TestForeignOwnershipIsNeverAdopted(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	in := bootstrap()
	name := ownedName("controller-1", in.Name)
	desc, _ := encodeOwnership(ownership{
		Schema: "semper-supra.garm-vm-owner/1", ManagedBy: "garm-provider-truenas",
		ControllerID: "other-controller", PoolID: in.PoolID, RunnerName: in.Name, Profile: FlavorLinuxGeneral,
	})
	client.items[7] = VM{
		ID: 7, Name: name, Description: desc, State: "STOPPED", Template: client.template,
		VCPUs: provider.GeneralCPU, MemoryBytes: provider.GeneralMemoryBytes,
	}
	_, err := manager.Create(context.Background(), in)
	if !errors.Is(err, provider.ErrForeign) {
		t.Fatalf("expected foreign ownership rejection, got %v", err)
	}
}

func TestDeleteRequiresStoppedStateAndCleansSeed(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	inst, err := manager.Create(context.Background(), bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), inst.ProviderID); !errors.Is(err, provider.ErrActive) {
		t.Fatalf("active delete should be refused, got %v", err)
	}
	if err := manager.Stop(context.Background(), inst.ProviderID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), inst.ProviderID); err != nil {
		t.Fatal(err)
	}
	if len(client.items) != 0 || len(client.seeds) != 0 {
		t.Fatalf("VM or seed residue remains: vms=%#v seeds=%#v", client.items, client.seeds)
	}
}

func TestUnknownRuntimeStateFailsClosed(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	desc, _ := encodeOwnership(ownership{
		Schema: "semper-supra.garm-vm-owner/1", ManagedBy: "garm-provider-truenas",
		ControllerID: "controller-1", PoolID: "pool-1", RunnerName: "runner-1", Profile: FlavorLinuxGeneral,
	})
	client.items[2] = VM{
		ID: 2, Name: "garm-owned", Description: desc, State: "PAUSED", Template: client.template,
		VCPUs: provider.GeneralCPU, MemoryBytes: provider.GeneralMemoryBytes,
	}
	if _, err := manager.List(context.Background(), ""); !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("unknown state should fail closed, got %v", err)
	}
}
