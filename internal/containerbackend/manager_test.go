package containerbackend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type fakeFile struct {
	content []byte
	mode    int
}

type fakeClient struct {
	version       string
	image         Image
	items         map[int]Container
	files         map[string]fakeFile
	nextID        int
	preserveState bool
	badMountpoint bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		version: "TrueNAS-26.0.0-BETA.3",
		image:   Image{Name: ImageFamily, Version: "20261001_07:42"},
		items:   map[int]Container{},
		files:   map[string]fakeFile{},
		nextID:  1,
	}
}

func (f *fakeClient) SystemVersion(context.Context) (string, error)       { return f.version, nil }
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
		Dataset: "tank/.truenas_containers/containers/" + spec.Name,
		Image:   spec.Image, Autostart: spec.Autostart, IDMapType: spec.IDMapType,
		CapabilitiesPolicy: spec.CapabilitiesPolicy, Init: spec.Init, InitEnv: cloneEnv(spec.InitEnv),
	}
	f.items[id] = item
	return item, nil
}
func (f *fakeClient) Update(_ context.Context, id int, spec UpdateSpec) (Container, error) {
	item, ok := f.items[id]
	if !ok {
		return Container{}, provider.ErrNotFound
	}
	if !f.preserveState {
		item.Init = spec.Init
		item.InitEnv = cloneEnv(spec.InitEnv)
		f.items[id] = item
	}
	return item, nil
}
func (f *fakeClient) ResolveDatasetMountpoint(_ context.Context, dataset string) (string, error) {
	if f.badMountpoint {
		return "/etc", nil
	}
	return "/mnt/" + dataset, nil
}
func (f *fakeClient) PutFile(_ context.Context, p string, content []byte, mode int) error {
	f.files[p] = fakeFile{content: append([]byte(nil), content...), mode: mode}
	return nil
}
func (f *fakeClient) StatFile(_ context.Context, p string) (FileStat, error) {
	file, ok := f.files[p]
	if !ok {
		return FileStat{}, provider.ErrNotFound
	}
	return FileStat{Size: int64(len(file.content)), Mode: file.mode}, nil
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
func (f *fakeClient) Delete(_ context.Context, id int) error { delete(f.items, id); return nil }

func bootstrap() provider.Bootstrap {
	return provider.Bootstrap{
		Name: "runner-1", OSType: "linux", Arch: "amd64", Flavor: FlavorLinuxGeneral,
		PoolID: "pool-1", CallbackURL: "https://garm.example/callback",
		MetadataURL: "https://garm.example/metadata", Token: "secret-bootstrap-token",
	}
}

func TestCreateStagesBootstrapAndScrubsDesiredState(t *testing.T) {
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
	if raw.State != "RUNNING" || raw.Init != DefaultInit || len(raw.InitEnv) != 0 {
		t.Fatalf("unexpected final state: %#v", raw)
	}
	if got.ProviderID != "container:1" || got.PoolID != "pool-1" {
		t.Fatalf("bad instance: %#v", got)
	}
	if len(client.files) != 2 {
		t.Fatalf("expected two staged files, got %d", len(client.files))
	}
	for p, file := range client.files {
		if strings.Contains(string(file.content), bootstrap().Token) {
			t.Fatalf("token leaked into %s", p)
		}
		if file.mode != 0o755 {
			t.Fatalf("bad mode at %s: %#o", p, file.mode)
		}
	}
}

func TestCreateCleansUpWhenScrubCannotBeProven(t *testing.T) {
	client := newFakeClient()
	client.preserveState = true
	manager, _ := New(client, "controller-1", client.version)
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected drift, got %v", err)
	}
	if len(client.items) != 0 {
		t.Fatalf("failed create residue: %#v", client.items)
	}
}

func TestCreateRejectsUnsafeMountpointAndCleansUp(t *testing.T) {
	client := newFakeClient()
	client.badMountpoint = true
	manager, _ := New(client, "controller-1", client.version)
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected drift, got %v", err)
	}
	if len(client.items) != 0 {
		t.Fatalf("unsafe staging residue: %#v", client.items)
	}
}

func TestCreateFailsClosedOnExactVersionMismatch(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	client.version = "TrueNAS-26.0.0-BETA.4"
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected version drift, got %v", err)
	}
}

func TestForeignOrInterruptedStateIsNeverAdopted(t *testing.T) {
	for _, tc := range []struct {
		name, controller string
		interrupted      bool
	}{
		{"foreign", "other-controller", false},
		{"interrupted", "controller-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeClient()
			manager, _ := New(client, "controller-1", client.version)
			in := bootstrap()
			name := ownedName("controller-1", in.Name)
			desc, _ := encodeOwnership(ownership{
				Schema: "semper-supra.garm-container-owner/1", ManagedBy: "garm-provider-truenas",
				ControllerID: tc.controller, PoolID: in.PoolID, RunnerName: in.Name, Profile: FlavorLinuxGeneral,
			})
			init := DefaultInit
			env := map[string]string{}
			state := "STOPPED"
			if tc.interrupted {
				plan := buildBootstrapPlan(in)
				init = plan.Init
				env = plan.InitEnv
				state = "RUNNING"
			}
			client.items[7] = Container{
				ID: 7, Name: name, Description: desc, State: state,
				Dataset: "tank/.truenas_containers/containers/" + name, Image: client.image,
				IDMapType: "DEFAULT", CapabilitiesPolicy: "DEFAULT", Init: init, InitEnv: env,
			}
			_, err := manager.Create(context.Background(), in)
			if tc.interrupted && !errors.Is(err, provider.ErrManagedDrift) {
				t.Fatalf("expected drift, got %v", err)
			}
			if !tc.interrupted && !errors.Is(err, provider.ErrForeign) {
				t.Fatalf("expected foreign, got %v", err)
			}
		})
	}
}

func TestDeleteRequiresStoppedStateAndUnknownStateFailsClosed(t *testing.T) {
	client := newFakeClient()
	manager, _ := New(client, "controller-1", client.version)
	inst, err := manager.Create(context.Background(), bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), inst.ProviderID); !errors.Is(err, provider.ErrActive) {
		t.Fatalf("active delete should fail, got %v", err)
	}
	if err := manager.Stop(context.Background(), inst.ProviderID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), inst.ProviderID); err != nil {
		t.Fatal(err)
	}

	desc, _ := encodeOwnership(ownership{
		Schema: "semper-supra.garm-container-owner/1", ManagedBy: "garm-provider-truenas",
		ControllerID: "controller-1", PoolID: "pool-1", RunnerName: "runner-1", Profile: FlavorLinuxGeneral,
	})
	client.items[2] = Container{
		ID: 2, Name: "garm-owned", Description: desc, State: "PAUSED",
		Dataset: "tank/.truenas_containers/containers/garm-owned", Image: client.image,
		IDMapType: "DEFAULT", CapabilitiesPolicy: "DEFAULT", Init: DefaultInit, InitEnv: map[string]string{},
	}
	if _, err := manager.List(context.Background(), ""); !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("unknown state should fail closed, got %v", err)
	}
}
