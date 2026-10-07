package containerbackend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type fakeClient struct {
	version      string
	image        Image
	items        map[int]Container
	nextID       int
	updateErr    error
	preserveToken bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		version: "TrueNAS-26.0.0-BETA.3",
		image:   Image{Name: ImageFamily, Version: "20261001_07:42"},
		items:   map[int]Container{},
		nextID:  1,
	}
}

func (f *fakeClient) SystemVersion(context.Context) (string, error) { return f.version, nil }
func (f *fakeClient) ResolveImage(context.Context, string) (Image, error) { return f.image, nil }
func (f *fakeClient) GetByName(_ context.Context, name string) (Container, error) {
	for _, item := range f.items {
		if item.Name == name {
			return item, nil
		}
	}
	return Container{}, provider.ErrNotFound
}
func (f *fakeClient) Get(_ context.Context, id int) (Container, error) {
	item, ok := f.items[id]
	if !ok {
		return Container{}, provider.ErrNotFound
	}
	return item, nil
}
func (f *fakeClient) List(context.Context) ([]Container, error) {
	out := make([]Container, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out, nil
}
func (f *fakeClient) Create(_ context.Context, spec CreateSpec) (Container, error) {
	id := f.nextID
	f.nextID++
	item := Container{
		ID: id, Name: spec.Name, Description: spec.Description, State: "STOPPED",
		Image: spec.Image, Autostart: spec.Autostart, IDMapType: spec.IDMapType,
		CapabilitiesPolicy: spec.CapabilitiesPolicy, InitEnv: cloneEnv(spec.InitEnv),
	}
	f.items[id] = item
	return item, nil
}
func (f *fakeClient) Update(_ context.Context, id int, spec UpdateSpec) (Container, error) {
	if f.updateErr != nil {
		return Container{}, f.updateErr
	}
	item, ok := f.items[id]
	if !ok {
		return Container{}, provider.ErrNotFound
	}
	if !f.preserveToken {
		item.InitEnv = cloneEnv(spec.InitEnv)
	}
	f.items[id] = item
	return item, nil
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

func TestCreateStartsOnceAndScrubsPersistedToken(t *testing.T) {
	client := newFakeClient()
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manager.Create(context.Background(), bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if raw.State != "RUNNING" {
		t.Fatalf("expected RUNNING after candidate bootstrap start, got %q", raw.State)
	}
	if tokenPresent(raw.InitEnv, bootstrap().Token) {
		t.Fatal("bootstrap token remained in persisted init environment")
	}
	if strings.Contains(raw.Description, bootstrap().Token) {
		t.Fatal("bootstrap token leaked into ownership metadata")
	}
	if raw.Autostart || raw.IDMapType != "DEFAULT" || raw.CapabilitiesPolicy != "DEFAULT" {
		t.Fatalf("security profile drift: %#v", raw)
	}
	if got.ProviderID != "container:1" || got.PoolID != "pool-1" {
		t.Fatalf("unexpected provider instance: %#v", got)
	}
}

func TestCreateFailsClosedWhenScrubCannotBeProven(t *testing.T) {
	client := newFakeClient()
	client.preserveToken = true
	manager, _ := New(client, "controller-1", client.version)
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected managed-drift scrub failure, got %v", err)
	}
}

func TestCreateFailsClosedOnExactVersionMismatch(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	client.version = "TrueNAS-26.0.0-BETA.4"
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected exact-version mismatch, got %v", err)
	}
}

func TestForeignOwnershipIsNeverAdopted(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	in := bootstrap()
	name := ownedName("controller-1", in.Name)
	desc, _ := encodeOwnership(ownership{
		Schema: "semper-supra.garm-container-owner/1", ManagedBy: "garm-provider-truenas",
		ControllerID: "other-controller", PoolID: in.PoolID, RunnerName: in.Name, Profile: FlavorLinuxGeneral,
	})
	client.items[7] = Container{
		ID: 7, Name: name, Description: desc, State: "STOPPED", Image: client.image,
		IDMapType: "DEFAULT", CapabilitiesPolicy: "DEFAULT",
	}
	_, err := manager.Create(context.Background(), in)
	if !errors.Is(err, provider.ErrForeign) {
		t.Fatalf("expected foreign ownership rejection, got %v", err)
	}
}

func TestStartIsUnsafeForOneJobRunner(t *testing.T) {
	manager, _ := New(newFakeClient(), "controller-1", "TrueNAS-26.0.0-BETA.3")
	if !errors.Is(manager.Start(context.Background(), "container:1"), provider.ErrUnsafeOperation) {
		t.Fatal("external restart must remain forbidden")
	}
}

func TestDeleteRequiresVerifiedStoppedStateAndAbsence(t *testing.T) {
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
	if len(client.items) != 0 {
		t.Fatalf("container residue remains: %#v", client.items)
	}
}

func TestUnknownRuntimeStateFailsClosed(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	desc, _ := encodeOwnership(ownership{
		Schema: "semper-supra.garm-container-owner/1", ManagedBy: "garm-provider-truenas",
		ControllerID: "controller-1", PoolID: "pool-1", RunnerName: "runner-1", Profile: FlavorLinuxGeneral,
	})
	client.items[2] = Container{
		ID: 2, Name: "garm-owned", Description: desc, State: "PAUSED", Image: client.image,
		IDMapType: "DEFAULT", CapabilitiesPolicy: "DEFAULT",
	}
	if _, err := manager.List(context.Background(), ""); !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("unknown state should fail closed, got %v", err)
	}
}
