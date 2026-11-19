package main

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"testing"

	garmErrors "github.com/cloudbase/garm-provider-common/errors"
	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/containerbackend"
	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type mockContainerClient struct {
	mu      sync.Mutex
	nextID  int
	version string
	items   map[int]containerbackend.Container
	files   map[string]containerbackend.FileStat
}

func newMockContainerClient() *mockContainerClient {
	return &mockContainerClient{
		nextID:  1,
		version: "TrueNAS-26.0.0-BETA.3",
		items:   make(map[int]containerbackend.Container),
		files:   make(map[string]containerbackend.FileStat),
	}
}

func (m *mockContainerClient) SystemVersion(context.Context) (string, error) {
	return m.version, nil
}

func (m *mockContainerClient) ResolveImage(_ context.Context, family string) (containerbackend.Image, error) {
	return containerbackend.Image{Name: family, Version: "20261118_07:42"}, nil
}

func (m *mockContainerClient) GetByName(_ context.Context, name string) (containerbackend.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.items {
		if item.Name == name {
			return item, nil
		}
	}
	return containerbackend.Container{}, provider.ErrNotFound
}

func (m *mockContainerClient) Get(_ context.Context, id int) (containerbackend.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return containerbackend.Container{}, provider.ErrNotFound
	}
	return item, nil
}

func (m *mockContainerClient) List(context.Context) ([]containerbackend.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]containerbackend.Container, 0, len(m.items))
	for _, item := range m.items {
		out = append(out, item)
	}
	return out, nil
}

func (m *mockContainerClient) Create(_ context.Context, spec containerbackend.CreateSpec) (containerbackend.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	envCopy := make(map[string]string, len(spec.InitEnv))
	for k, v := range spec.InitEnv {
		envCopy[k] = v
	}
	item := containerbackend.Container{
		ID:                 id,
		Name:               spec.Name,
		Description:        spec.Description,
		State:              "STOPPED",
		Dataset:            path.Join("rdtepool/.truenas_containers/containers", spec.Name),
		Image:              spec.Image,
		Autostart:          spec.Autostart,
		IDMapType:          spec.IDMapType,
		CapabilitiesPolicy: spec.CapabilitiesPolicy,
		Init:               spec.Init,
		InitEnv:            envCopy,
	}
	m.items[id] = item
	return item, nil
}

func (m *mockContainerClient) Update(_ context.Context, id int, spec containerbackend.UpdateSpec) (containerbackend.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return containerbackend.Container{}, provider.ErrNotFound
	}
	item.Init = spec.Init
	envCopy := make(map[string]string, len(spec.InitEnv))
	for k, v := range spec.InitEnv {
		envCopy[k] = v
	}
	item.InitEnv = envCopy
	m.items[id] = item
	return item, nil
}

func (m *mockContainerClient) PutFile(_ context.Context, filePath string, data []byte, mode int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[filePath] = containerbackend.FileStat{Size: int64(len(data)), Mode: mode}
	return nil
}

func (m *mockContainerClient) StatFile(_ context.Context, filePath string) (containerbackend.FileStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.files[filePath]
	if !ok {
		return containerbackend.FileStat{}, provider.ErrNotFound
	}
	return st, nil
}

func (m *mockContainerClient) Start(_ context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	item.State = "RUNNING"
	m.items[id] = item
	// Simulate the init wrapper consuming and deleting the one-shot token file.
	for fp := range m.files {
		if strings.HasSuffix(fp, "/var/lib/garm-container/bootstrap-instance-token") {
			delete(m.files, fp)
		}
	}
	return nil
}

func (m *mockContainerClient) Stop(_ context.Context, id int, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	item.State = "STOPPED"
	m.items[id] = item
	return nil
}

func (m *mockContainerClient) Delete(_ context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return provider.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func validContainerBootstrap(name string) garmParams.BootstrapInstance {
	b := validBootstrap()
	b.Name = name
	b.Flavor = containerbackend.FlavorLinuxGeneral
	b.Image = containerbackend.ImageFamily
	b.PoolID = "pool-container-1"
	return b
}

func TestGARMExternalProviderContainerBackendAdapterLifecycle(t *testing.T) {
	ctx := context.Background()
	client := newMockContainerClient()
	mgr, err := containerbackend.New(client, "ctrl-container-test", "TrueNAS-26.0.0-BETA.3")
	if err != nil {
		t.Fatalf("containerbackend.New: %v", err)
	}

	ep := &externalProvider{
		cfg: config{
			Mode:    "truenas",
			TrueNAS: &trueNASConfig{Host: "truenas.example.invalid", Username: "garm", Backend: trueNASBackendContainer},
		},
		controllerID: "ctrl-container-test",
		managerFactory: func(context.Context) (instanceManager, func() error, error) {
			return mgr, nil, nil
		},
	}

	// Cross-backend Apps image/flavor must be rejected fail-closed.
	badBootstrap := validContainerBootstrap("runner-cross-backend")
	badBootstrap.Flavor = provider.FlavorLinuxGeneral
	if _, err := ep.CreateInstance(ctx, badBootstrap); !errors.Is(err, garmErrors.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest for cross-backend Apps flavor, got %v", err)
	}

	// CreateInstance -> GetInstance -> ListInstances -> Stop -> Start (unsupported) -> DeleteInstance.
	created, err := ep.CreateInstance(ctx, validContainerBootstrap("runner-c1"))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if created.Status != garmParams.InstanceRunning {
		t.Fatalf("created status = %q, want running", created.Status)
	}

	got, err := ep.GetInstance(ctx, created.ProviderID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got.ProviderID != created.ProviderID {
		t.Fatalf("GetInstance ProviderID = %q, want %q", got.ProviderID, created.ProviderID)
	}

	listed, err := ep.ListInstances(ctx, "pool-container-1")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(listed) != 1 || listed[0].ProviderID != created.ProviderID {
		t.Fatalf("ListInstances = %+v, want 1 matching container", listed)
	}

	if err := ep.Stop(ctx, created.ProviderID, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := ep.Start(ctx, created.ProviderID); !errors.Is(err, garmErrors.ErrBadRequest) {
		t.Fatalf("Start on ephemeral container must fail closed with ErrBadRequest, got %v", err)
	}

	if err := ep.DeleteInstance(ctx, created.ProviderID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if _, err := ep.GetInstance(ctx, created.ProviderID); !errors.Is(err, garmErrors.ErrNotFound) {
		t.Fatalf("GetInstance after Delete = %v, want ErrNotFound", err)
	}
}
