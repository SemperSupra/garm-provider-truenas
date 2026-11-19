package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	garmErrors "github.com/cloudbase/garm-provider-common/errors"
	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/vmbackend"
)

type mockVMClient struct {
	mu      sync.Mutex
	nextID  int
	version string
	items   map[int]vmbackend.VM
	seeds   map[string]vmbackend.SeedSpec
}

func newMockVMClient(version string) *mockVMClient {
	return &mockVMClient{
		nextID:  1,
		version: version,
		items:   make(map[int]vmbackend.VM),
		seeds:   make(map[string]vmbackend.SeedSpec),
	}
}

func (m *mockVMClient) SystemVersion(context.Context) (string, error) {
	return m.version, nil
}

func (m *mockVMClient) ResolveTemplate(_ context.Context, family string) (vmbackend.Template, error) {
	return vmbackend.Template{
		ID:           100,
		Name:         family,
		RuntimeName:  vmbackend.TemplateRuntimeName,
		Version:      vmbackend.TemplateVersion,
		SourceURL:    vmbackend.TemplateSourceURL,
		SourceSHA256: vmbackend.TemplateSourceSHA256,
	}, nil
}

func (m *mockVMClient) GetByName(_ context.Context, name string) (vmbackend.VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.items {
		if item.Name == name {
			return item, nil
		}
	}
	return vmbackend.VM{}, provider.ErrNotFound
}

func (m *mockVMClient) Get(_ context.Context, id int) (vmbackend.VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return vmbackend.VM{}, provider.ErrNotFound
	}
	return item, nil
}

func (m *mockVMClient) List(context.Context) ([]vmbackend.VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]vmbackend.VM, 0, len(m.items))
	for _, item := range m.items {
		out = append(out, item)
	}
	return out, nil
}

func (m *mockVMClient) Clone(_ context.Context, _ int, spec vmbackend.CloneSpec) (vmbackend.VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	item := vmbackend.VM{
		ID:          id,
		Name:        spec.Name,
		Description: spec.Description,
		State:       "STOPPED",
		Autostart:   spec.Autostart,
		VCPUs:       spec.VCPUs,
		MemoryBytes: spec.MemoryBytes,
		Template: vmbackend.Template{
			ID:           100,
			Name:         vmbackend.TemplateFamily,
			RuntimeName:  vmbackend.TemplateRuntimeName,
			Version:      vmbackend.TemplateVersion,
			SourceURL:    vmbackend.TemplateSourceURL,
			SourceSHA256: vmbackend.TemplateSourceSHA256,
		},
	}
	m.items[id] = item
	return item, nil
}

func (m *mockVMClient) StageSeed(_ context.Context, vmID int, spec vmbackend.SeedSpec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := fmt.Sprintf("rdtepool/garm-vm-seed-%d", vmID)
	m.seeds[ref] = spec
	return ref, nil
}

func (m *mockVMClient) AttachSeed(_ context.Context, vmID int, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[vmID]
	if !ok {
		return provider.ErrNotFound
	}
	item.SeedRef = ref
	m.items[vmID] = item
	return nil
}

func (m *mockVMClient) DetachSeed(_ context.Context, vmID int, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[vmID]
	if !ok {
		return provider.ErrNotFound
	}
	if item.SeedRef == ref {
		item.SeedRef = ""
		m.items[vmID] = item
	}
	return nil
}

func (m *mockVMClient) DeleteSeed(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seeds, ref)
	return nil
}

func (m *mockVMClient) BootstrapConsumed(context.Context, int) (bool, error) {
	return true, nil
}

func (m *mockVMClient) Start(_ context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return provider.ErrNotFound
	}
	item.State = "RUNNING"
	m.items[id] = item
	return nil
}

func (m *mockVMClient) Stop(_ context.Context, id int, _ bool) error {
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

func (m *mockVMClient) Delete(_ context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return provider.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func validVMBootstrap(name string) garmParams.BootstrapInstance {
	b := validBootstrap()
	b.Name = name
	b.Flavor = vmbackend.FlavorLinuxGeneral
	b.Image = vmbackend.TemplateFamily
	b.PoolID = "pool-vm-1"
	return b
}

func TestGARMExternalProviderVMBackendAdapterLifecycle(t *testing.T) {
	ctx := context.Background()

	// 25.04.1 must remain explicitly NOT_ADMITTED for VM backend.
	if _, err := vmbackend.New(newMockVMClient("TrueNAS-25.04.1"), "ctrl-vm-test", "TrueNAS-25.04.1"); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("expected 25.04.1 VM backend to fail closed with ErrUnsupported, got %v", err)
	}

	for _, sysVer := range []string{"TrueNAS-26.0.0-BETA.3", "TrueNAS-25.10.7", "TrueNAS-25.04.2.6"} {
		t.Run(sysVer, func(t *testing.T) {
			client := newMockVMClient(sysVer)
			mgr, err := vmbackend.New(client, "ctrl-vm-test", sysVer)
			if err != nil {
				t.Fatalf("vmbackend.New(%s): %v", sysVer, err)
			}

			ep := &externalProvider{
				cfg: config{
					Mode:    "truenas",
					TrueNAS: &trueNASConfig{Host: "truenas.example.invalid", Username: "garm", Backend: trueNASBackendVM},
				},
				controllerID: "ctrl-vm-test",
				managerFactory: func(context.Context) (instanceManager, func() error, error) {
					return mgr, nil, nil
				},
			}

			// Cross-backend Apps image/flavor must be rejected fail-closed.
			badBootstrap := validVMBootstrap("runner-cross-backend")
			badBootstrap.Flavor = provider.FlavorLinuxGeneral
			if _, err := ep.CreateInstance(ctx, badBootstrap); !errors.Is(err, garmErrors.ErrBadRequest) {
				t.Fatalf("expected ErrBadRequest for cross-backend Apps flavor, got %v", err)
			}

			created, err := ep.CreateInstance(ctx, validVMBootstrap("runner-v1"))
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

			listed, err := ep.ListInstances(ctx, "pool-vm-1")
			if err != nil {
				t.Fatalf("ListInstances: %v", err)
			}
			if len(listed) != 1 || listed[0].ProviderID != created.ProviderID {
				t.Fatalf("ListInstances = %+v, want 1 matching VM", listed)
			}

			if err := ep.Stop(ctx, created.ProviderID, false); err != nil {
				t.Fatalf("Stop: %v", err)
			}

			if err := ep.Start(ctx, created.ProviderID); !errors.Is(err, garmErrors.ErrBadRequest) {
				t.Fatalf("Start on ephemeral VM must fail closed with ErrBadRequest, got %v", err)
			}

			if err := ep.DeleteInstance(ctx, created.ProviderID); err != nil {
				t.Fatalf("DeleteInstance: %v", err)
			}

			if _, err := ep.GetInstance(ctx, created.ProviderID); !errors.Is(err, garmErrors.ErrNotFound) {
				t.Fatalf("GetInstance after Delete = %v, want ErrNotFound", err)
			}
		})
	}
}
