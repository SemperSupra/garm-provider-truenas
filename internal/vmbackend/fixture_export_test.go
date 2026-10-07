package vmbackend

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/truenasstore"
)

type nestedVMPreB4Fixture struct {
	Schema              string            `json:"schema"`
	ProducerSource      string            `json:"producer_source"`
	Authority           string            `json:"authority"`
	Target              nestedVMTarget    `json:"target"`
	Profile             string            `json:"profile"`
	TemplateFamily      string            `json:"template_family"`
	TemplateVersion     string            `json:"template_version"`
	TemplateRuntimeName string            `json:"template_runtime_name"`
	TemplateSourceURL   string            `json:"template_source_url"`
	TemplateSourceSHA   string            `json:"template_source_sha256"`
	ExpectedName        string            `json:"expected_name"`
	ExpectedOwnership   map[string]string `json:"expected_ownership"`
	Clone               nestedVMClone     `json:"clone"`
	Seed                nestedVMSeed      `json:"seed"`
	Retirement          []string          `json:"retirement"`
	Runner              nestedVMRunner    `json:"runner"`
	SourceOracles       map[string]bool   `json:"source_oracles"`
	ClaimBoundary       string            `json:"claim_boundary"`
}

type nestedVMTarget struct {
	Version          string            `json:"version"`
	SystemVersion    string            `json:"system_version"`
	Driver           string            `json:"driver"`
	ControlSurface   string            `json:"control_surface"`
	MiddlewareCommit string            `json:"middleware_commit"`
	RequiredMethods  []string          `json:"required_methods"`
	SourceBlobs      map[string]string `json:"source_blobs"`
	Status           string            `json:"status"`
}

type nestedVMClone struct {
	Description string `json:"description"`
	VCPUs       int    `json:"vcpus"`
	MemoryBytes int64  `json:"memory_bytes"`
	Autostart   bool   `json:"autostart"`
}

type nestedVMSeed struct {
	MetaData                  string   `json:"meta_data"`
	UserData                  string   `json:"user_data"`
	RunLocalTokenPlaceholder  string   `json:"run_local_token_placeholder"`
	Transport                 string   `json:"transport"`
	AttachDevice              string   `json:"attach_device"`
	RetainWhileActive         bool     `json:"retain_while_active"`
	DeleteOnlyWhenStopped     bool     `json:"delete_only_when_stopped"`
	ConsumptionSignalRequired bool     `json:"consumption_signal_required"`
	ConsumptionMarker         string   `json:"consumption_marker"`
	RequiredStages            []string `json:"required_stages"`
}

type nestedVMRunner struct {
	ToolURL      string `json:"tool_url"`
	ToolFilename string `json:"tool_filename"`
	ToolSHA256   string `json:"tool_sha256"`
}

func TestExportNestedVMPreB4Fixture(t *testing.T) {
	out := strings.TrimSpace(os.Getenv("GARM_VM_FIXTURE_OUT"))
	if out == "" {
		t.Skip("GARM_VM_FIXTURE_OUT is not set")
	}
	producer := strings.TrimSpace(os.Getenv("GARM_VM_FIXTURE_SOURCE"))
	if len(producer) != 40 {
		t.Fatalf("GARM_VM_FIXTURE_SOURCE must be exact 40-char commit, got %q", producer)
	}

	matrix, err := truenasstore.LoadRuntimeBackendMatrix()
	if err != nil {
		t.Fatal(err)
	}
	cell := matrix.Backends["vm"].TargetStatus["26.0.0-BETA.3"]
	if cell.Status != "OPEN" || cell.Driver != "vm-v1" || cell.ControlSurface != "vm.*" {
		t.Fatalf("unexpected BETA.3 VM cell: %#v", cell)
	}

	in := provider.Bootstrap{
		Name:        "nested-vm-runner",
		OSType:      "linux",
		Arch:        "amd64",
		Flavor:      FlavorLinuxGeneral,
		PoolID:      "nested-vm-pool",
		CallbackURL: "http://10.47.214.1:9443/callback",
		MetadataURL: "http://10.47.214.1:9443/metadata",
		Token:       "__RUN_LOCAL_INSTANCE_TOKEN__",
	}
	if err := validateBootstrap(in); err != nil {
		t.Fatal(err)
	}

	name := ownedName("nested-vm-controller", in.Name)
	desc, err := encodeOwnership(ownership{
		Schema:       "semper-supra.garm-vm-owner/1",
		ManagedBy:    "garm-provider-truenas",
		ControllerID: "nested-vm-controller",
		PoolID:       in.PoolID,
		RunnerName:   in.Name,
		Profile:      FlavorLinuxGeneral,
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := seedFor(in, name)
	if !strings.Contains(seed.UserData, in.Token) {
		t.Fatal("NoCloud user-data lost run-local token placeholder")
	}
	if strings.Contains(desc, in.Token) || strings.Contains(seed.MetaData, in.Token) {
		t.Fatal("run-local token leaked outside NoCloud user-data")
	}

	fixture := nestedVMPreB4Fixture{
		Schema:         "semper-supra.garm-provider-truenas-vm-pre-b4-fixture/1",
		ProducerSource: producer,
		Authority:      "SemperSupra/garm-provider-truenas-private#43",
		Target: nestedVMTarget{
			Version:          "26.0.0-BETA.3",
			SystemVersion:    "TrueNAS-26.0.0-BETA.3",
			Driver:           cell.Driver,
			ControlSurface:   cell.ControlSurface,
			MiddlewareCommit: cell.MiddlewareCommit,
			RequiredMethods:  append([]string(nil), cell.RequiredMethods...),
			SourceBlobs:      cell.SourceBlobs,
			Status:           cell.Status,
		},
		Profile:             FlavorLinuxGeneral,
		TemplateFamily:      TemplateFamily,
		TemplateVersion:     TemplateVersion,
		TemplateRuntimeName: TemplateRuntimeName,
		TemplateSourceURL:   TemplateSourceURL,
		TemplateSourceSHA:   TemplateSourceSHA256,
		ExpectedName:        name,
		ExpectedOwnership: map[string]string{
			"schema":        "semper-supra.garm-vm-owner/1",
			"managed_by":    "garm-provider-truenas",
			"controller_id": "nested-vm-controller",
			"pool_id":       in.PoolID,
			"runner_name":   in.Name,
			"profile":       FlavorLinuxGeneral,
		},
		Clone: nestedVMClone{
			Description: desc,
			VCPUs:       provider.GeneralCPU,
			MemoryBytes: provider.GeneralMemoryBytes,
			Autostart:   false,
		},
		Seed: nestedVMSeed{
			MetaData:                  seed.MetaData,
			UserData:                  seed.UserData,
			RunLocalTokenPlaceholder:  in.Token,
			Transport:                 "provider-owned child dataset + public filesystem.put input pipe",
			AttachDevice:              "owned vm.device CDROM",
			RetainWhileActive:         true,
			DeleteOnlyWhenStopped:     true,
			ConsumptionSignalRequired: true,
			ConsumptionMarker:         seed.ConsumptionMarker,
			RequiredStages: []string{
				"create provider-owned seed child dataset",
				"generate deterministic NoCloud ISO from exact fixture",
				"upload ISO via public filesystem.put",
				"attach owned CDROM via vm.device.create",
				"read back attached device before start",
				"start cloned VM",
				"observe independent bootstrap-consumption signal",
			},
		},
		Retirement: []string{
			"stop VM through vm.stop",
			"verify STOPPED",
			"delete owned NoCloud CDROM through vm.device.delete",
			"verify seed device absence",
			"delete provider-owned seed dataset",
			"verify seed dataset absence",
			"delete provider-owned VM and cloned boot ZVOL",
			"verify final VM absence",
		},
		Runner: nestedVMRunner{
			ToolURL:      provider.RunnerToolURL,
			ToolFilename: provider.RunnerToolFilename,
			ToolSHA256:   provider.RunnerToolSHA256,
		},
		SourceOracles: map[string]bool{
			"exact_version_required":                true,
			"25_04_1_not_admitted":                  true,
			"foreign_ownership_rejected":            true,
			"classic_vm_name_safe":                  true,
			"fixed_cpu_memory_profile_required":     true,
			"autostart_forbidden":                   true,
			"zvol_template_clone_required":          true,
			"supported_filesystem_put_required":     true,
			"stock_template_source_exact":           true,
			"self_contained_nocloud_bootstrap":      true,
			"unprivileged_runner_required":          true,
			"owned_seed_dataset_required":           true,
			"owned_cdrom_required":                  true,
			"active_seed_detach_forbidden":          true,
			"bootstrap_consumption_signal_required": true,
			"console_consumption_marker_required":   true,
			"seed_device_absence_required":          true,
			"seed_dataset_absence_required":         true,
			"final_vm_absence_required":             true,
			"runtime_admission_claimed":             false,
			"guest_boot_claimed":                    false,
			"bootstrap_consumption_claimed":         false,
			"github_jit_boundary_claimed":           false,
			"docker_or_container_actions_claimed":   false,
			"windows_or_gpu_claimed":                false,
		},
		ClaimBoundary: "Source/static BETA.3 classic vm-v1 pre-B4 fixture. It binds exact target/source identity, classic-vm-safe runtime naming, provider ownership, fixed 4-vCPU/8-GiB clone profile, a dated SHA256-pinned stock Ubuntu 24.04 cloud-image template with exact TrueNAS runtime name, self-contained NoCloud bootstrap scripts with only a literal run-local token placeholder and a deterministic post-secret-deletion console marker, supported filesystem.put + vm.device + pool.dataset control surfaces, stopped-state seed retirement, and zero-residue requirements. It does not prove template availability, VM guest boot, bootstrap consumption, callback/JIT, GitHub registration, Docker/container-actions, Windows/GPU, physical TrueNAS, or operational runtime admission.",
	}

	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
