package provider

import (
	"context"
	"errors"
	"testing"
)

type fakeClient struct {
	apps        map[string]App
	createCount int
	deleteCount int
}

func newFakeClient() *fakeClient { return &fakeClient{apps: map[string]App{}} }

func (f *fakeClient) CreateApp(_ context.Context, spec AppSpec) (App, error) {
	f.createCount++
	app := App{Spec: spec, State: StateRunning}
	f.apps[spec.Name] = app
	return app, nil
}

func (f *fakeClient) GetApp(_ context.Context, name string) (App, error) {
	app, ok := f.apps[name]
	if !ok {
		return App{}, ErrNotFound
	}
	return app, nil
}

func (f *fakeClient) ListApps(context.Context) ([]App, error) {
	out := make([]App, 0, len(f.apps))
	for _, app := range f.apps {
		out = append(out, app)
	}
	return out, nil
}

func (f *fakeClient) DeleteApp(_ context.Context, name string) error {
	f.deleteCount++
	delete(f.apps, name)
	return nil
}

func validBootstrap() Bootstrap {
	return Bootstrap{
		Name:        "garm-test-runner",
		OSType:      "linux",
		Arch:        "amd64",
		Flavor:      FlavorLinuxGeneral,
		PoolID:      "pool-1",
		CallbackURL: "https://garm.example.invalid/api/v1/callbacks",
		MetadataURL: "https://garm.example.invalid/api/v1/metadata",
		Token:       "test-only-token",
	}
}

func TestCreateUsesLockedSecurityProfile(t *testing.T) {
	client := newFakeClient()
	manager, err := NewManager(client, "controller-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.Create(context.Background(), validBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	app := client.apps[instance.ProviderID]
	if app.Spec.Image != RunnerImage {
		t.Fatalf("unexpected image: %s", app.Spec.Image)
	}
	if app.Spec.CPU != GeneralCPU || app.Spec.MemoryBytes != GeneralMemoryBytes {
		t.Fatalf("unexpected resource profile: %#v", app.Spec)
	}
	if app.Spec.RunAsUser != "1001:1001" || !app.Spec.NoNewPrivileges || !app.Spec.WorkdirTmpfs {
		t.Fatalf("security invariants missing: %#v", app.Spec)
	}
	if len(app.Spec.CapDrop) != 1 || app.Spec.CapDrop[0] != "ALL" {
		t.Fatalf("capabilities not dropped: %#v", app.Spec.CapDrop)
	}
	if len(app.Spec.HostMounts) != 0 || app.Spec.DockerSocket {
		t.Fatalf("host access unexpectedly enabled: %#v", app.Spec)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-1")
	first, err := manager.Create(context.Background(), validBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(context.Background(), validBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	if first.ProviderID != second.ProviderID {
		t.Fatalf("provider id changed: %q != %q", first.ProviderID, second.ProviderID)
	}
	if client.createCount != 1 {
		t.Fatalf("expected one create, got %d", client.createCount)
	}
}

func TestDeleteRefusesActiveRunner(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-1")
	instance, err := manager.Create(context.Background(), validBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), instance.ProviderID); !errors.Is(err, ErrActive) {
		t.Fatalf("expected ErrActive, got %v", err)
	}
	if client.deleteCount != 0 {
		t.Fatalf("active runner was deleted")
	}
}

func TestDeleteAllowsStoppedOwnedRunner(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-1")
	instance, err := manager.Create(context.Background(), validBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	app := client.apps[instance.ProviderID]
	app.State = StateStopped
	client.apps[instance.ProviderID] = app
	if err := manager.Delete(context.Background(), instance.ProviderID); err != nil {
		t.Fatal(err)
	}
	if client.deleteCount != 1 {
		t.Fatalf("expected one delete, got %d", client.deleteCount)
	}
}

func TestForeignInstanceCannotBeDeleted(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-a")
	client.apps["foreign"] = App{Spec: AppSpec{Name: "foreign", ControllerID: "controller-b", PoolID: "pool-1"}, State: StateStopped}
	if err := manager.Delete(context.Background(), "foreign"); !errors.Is(err, ErrForeign) {
		t.Fatalf("expected ErrForeign, got %v", err)
	}
	if client.deleteCount != 0 {
		t.Fatalf("foreign instance was deleted")
	}
}

func TestListFiltersControllerAndPool(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-a")
	client.apps["mine"] = App{Spec: AppSpec{Name: "mine", ControllerID: "controller-a", PoolID: "pool-a"}, State: StateRunning}
	client.apps["other-pool"] = App{Spec: AppSpec{Name: "other-pool", ControllerID: "controller-a", PoolID: "pool-b"}, State: StateRunning}
	client.apps["foreign"] = App{Spec: AppSpec{Name: "foreign", ControllerID: "controller-b", PoolID: "pool-a"}, State: StateRunning}
	instances, err := manager.List(context.Background(), "pool-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ProviderID != "mine" {
		t.Fatalf("unexpected instances: %#v", instances)
	}
}

func TestUnsupportedRequestFailsClosed(t *testing.T) {
	client := newFakeClient()
	manager, _ := NewManager(client, "controller-1")
	in := validBootstrap()
	in.Flavor = "arbitrary-compose"
	if _, err := manager.Create(context.Background(), in); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}
