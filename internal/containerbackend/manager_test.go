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
	badDataset    bool
	deleteNoop    bool
}

type createReadbackAdversary struct {
	*fakeClient
}

func (c *createReadbackAdversary) Create(ctx context.Context, spec CreateSpec) (Container, error) {
	item, err := c.fakeClient.Create(ctx, spec)
	if err != nil {
		return item, err
	}
	item.State = "RUNNING"
	c.items[item.ID] = item
	return item, nil
}

type cleanupAdversary struct {
	*fakeClient
	getCalls    int
	mutateAt    int
	stopCalls   int
	deleteCalls int
}

func (c *cleanupAdversary) Get(ctx context.Context, id int) (Container, error) {
	c.getCalls++
	if c.getCalls == c.mutateAt {
		item := c.items[id]
		item.Description = `{"schema":"semper-supra.garm-container-owner/1","managed_by":"garm-provider-truenas","controller_id":"foreign-controller","pool_id":"pool-1","runner_name":"runner-1","profile":"truenas-container-linux-general"}`
		c.items[id] = item
	}
	return c.fakeClient.Get(ctx, id)
}

func (c *cleanupAdversary) Stop(ctx context.Context, id int, force bool) error {
	c.stopCalls++
	return c.fakeClient.Stop(ctx, id, force)
}

func (c *cleanupAdversary) Delete(ctx context.Context, id int) error {
	c.deleteCalls++
	return c.fakeClient.Delete(ctx, id)
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
	dataset := "tank/.truenas_containers/containers/" + spec.Name
	if f.badDataset {
		dataset = "tank/foreign_layout/" + spec.Name
	}
	item := Container{
		ID: id, Name: spec.Name, Description: spec.Description, State: "STOPPED",
		Dataset: dataset,
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
	if item.Init == BootstrapInitCommand {
		root, _ := containerRootfsMountpoint(item.Dataset, item.Name)
		delete(f.files, root+BootstrapTokenPath)
		f.files[root+InitWrapperMarkerPath] = fakeFile{content: []byte("init-wrapper-v1\n"), mode: 0o644}
		f.files[root+BootstrapChildMarkerPath] = fakeFile{content: []byte("bootstrap-child-v1\n"), mode: 0o644}
	}
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
	if !f.deleteNoop {
		delete(f.items, id)
	}
	return nil
}

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
	root, _ := containerRootfsMountpoint(raw.Dataset, raw.Name)
	if _, ok := client.files[root+BootstrapTokenPath]; ok {
		t.Fatal("one-shot credential file remained after wrapper execution")
	}
	for _, marker := range []string{InitWrapperMarkerPath, BootstrapChildMarkerPath} {
		if _, ok := client.files[root+marker]; !ok {
			t.Fatalf("execution marker missing: %s", marker)
		}
	}
	for _, script := range []string{BootstrapRunnerScriptPath, BootstrapInitScriptPath} {
		file, ok := client.files[root+script]
		if !ok {
			t.Fatalf("staged script missing: %s", script)
		}
		if strings.Contains(string(file.content), bootstrap().Token) {
			t.Fatalf("credential leaked into %s", script)
		}
		if file.mode != 0o755 {
			t.Fatalf("bad mode at %s: %#o", script, file.mode)
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

func TestCreateRejectsUnexpectedRootDatasetAndCleansUp(t *testing.T) {
	client := newFakeClient()
	client.badDataset = true
	manager, _ := New(client, "controller-1", client.version)
	_, err := manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("expected drift, got %v", err)
	}
	if len(client.items) != 0 {
		t.Fatalf("unsafe staging residue: %#v", client.items)
	}
}

func TestFailedCreateCleanupRechecksIdentityBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutateAt  int
		wantStops int
	}{
		{"before stop", 1, 0},
		{"before delete", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &cleanupAdversary{fakeClient: newFakeClient()}
			manager, err := New(client, "controller-1", client.version)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Create(context.Background(), bootstrap()); err != nil {
				t.Fatal(err)
			}
			created := client.items[1]
			client.getCalls = 0
			client.mutateAt = tc.mutateAt
			if err := manager.cleanupFailedCreate(context.Background(), created, bootstrap().PoolID); !errors.Is(err, provider.ErrForeign) {
				t.Fatalf("changed identity must preserve uncertain residue, got %v", err)
			}
			if client.stopCalls != tc.wantStops || client.deleteCalls != 0 {
				t.Fatalf("unexpected destructive calls: stop=%d delete=%d", client.stopCalls, client.deleteCalls)
			}
			if _, ok := client.items[created.ID]; !ok {
				t.Fatal("foreign replacement was deleted")
			}
		})
	}
}

func TestCreateReportsUncertainCleanupResidue(t *testing.T) {
	client := &cleanupAdversary{fakeClient: newFakeClient(), mutateAt: 2}
	client.preserveState = true
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) || !errors.Is(err, provider.ErrForeign) {
		t.Fatalf("create must report both primary drift and uncertain foreign cleanup: %v", err)
	}
	if client.stopCalls != 0 || client.deleteCalls != 0 {
		t.Fatalf("uncertain container was mutated: stop=%d delete=%d", client.stopCalls, client.deleteCalls)
	}
	if _, ok := client.items[1]; !ok {
		t.Fatal("uncertain residue was deleted")
	}
}

func TestFailedCreateCleanupRequiresObservedFinalAbsence(t *testing.T) {
	client := newFakeClient()
	client.preserveState = true
	client.deleteNoop = true
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) || !strings.Contains(err.Error(), "uncertain residue") {
		t.Fatalf("delete acknowledgement without absence must report uncertainty: %v", err)
	}
	if _, ok := client.items[1]; !ok {
		t.Fatal("fake retained container unexpectedly absent")
	}

	client.deleteNoop = false
	if err := manager.cleanupFailedCreate(context.Background(), client.items[1], bootstrap().PoolID); err != nil {
		t.Fatalf("actual deletion and final ID/name absence should pass: %v", err)
	}
}

func TestFailedCreateCleanupDoesNotTrustIDAbsenceAlone(t *testing.T) {
	client := newFakeClient()
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), bootstrap()); err != nil {
		t.Fatal(err)
	}
	created := client.items[1]
	delete(client.items, 1)
	renumbered := created
	renumbered.ID = 2
	client.items[2] = renumbered
	if err := manager.cleanupFailedCreate(context.Background(), created, bootstrap().PoolID); !errors.Is(err, provider.ErrManagedDrift) {
		t.Fatalf("owned name under new ID must remain uncertain, got %v", err)
	}
	if _, ok := client.items[2]; !ok {
		t.Fatal("renumbered container was deleted")
	}
}

func TestCreateEarlyUnsafeReadbackReportsPossibleResidue(t *testing.T) {
	client := &createReadbackAdversary{fakeClient: newFakeClient()}
	manager, err := New(client, "controller-1", client.version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create(context.Background(), bootstrap())
	if !errors.Is(err, provider.ErrManagedDrift) || !strings.Contains(err.Error(), "residue may remain") {
		t.Fatalf("early post-create stop must report possible residue: %v", err)
	}
	if _, ok := client.items[1]; !ok {
		t.Fatal("unproven active container was deleted")
	}
}

func TestContainerRootfsProjectionMatchesExactBeta3Source(t *testing.T) {
	got, err := containerRootfsMountpoint(
		"tank/.truenas_containers/containers/garm_runner_1234",
		"garm_runner_1234",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/mnt/.truenas_containers/tank/containers/garm_runner_1234" {
		t.Fatalf("unexpected source-derived mountpoint %q", got)
	}
	for _, dataset := range []string{
		"./.truenas_containers/containers/garm_runner_1234",
		"../.truenas_containers/containers/garm_runner_1234",
		"/tank/.truenas_containers/containers/garm_runner_1234",
		"tank/.truenas_containers/containers/garm_runner_1234/",
		"tank/containers/garm_runner_1234",
		"tank/.truenas_containers/foreign/garm_runner_1234",
		"tank/.truenas_containers/containers/other",
		"/etc",
	} {
		if _, err := containerRootfsMountpoint(dataset, "garm_runner_1234"); !errors.Is(err, provider.ErrManagedDrift) {
			t.Fatalf("expected source-projection drift for %q, got %v", dataset, err)
		}
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
