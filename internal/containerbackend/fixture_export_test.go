package containerbackend

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/truenasstore"
)

type nestedContainerPreB4Fixture struct {
	Schema                   string                       `json:"schema"`
	ProducerSource           string                       `json:"producer_source"`
	Authority                string                       `json:"authority"`
	Target                   nestedContainerTarget        `json:"target"`
	Profile                  string                       `json:"profile"`
	ImageFamily              string                       `json:"image_family"`
	ExpectedName             string                       `json:"expected_name"`
	ExpectedOwnership        map[string]string            `json:"expected_ownership"`
	DesiredCreate            nestedContainerDesiredCreate `json:"desired_create"`
	ExpectedPostStartInitEnv map[string]string            `json:"expected_post_start_initenv"`
	Runner                   nestedContainerRunner        `json:"runner"`
	SourceOracles            map[string]bool              `json:"source_oracles"`
	ClaimBoundary            string                       `json:"claim_boundary"`
}

type nestedContainerTarget struct {
	Version          string            `json:"version"`
	SystemVersion    string            `json:"system_version"`
	Driver           string            `json:"driver"`
	ControlSurface   string            `json:"control_surface"`
	MiddlewareCommit string            `json:"middleware_commit"`
	RequiredMethods  []string          `json:"required_methods"`
	SourceBlobs      map[string]string `json:"source_blobs"`
	Status           string            `json:"status"`
}

type nestedContainerDesiredCreate struct {
	Description        string            `json:"description"`
	Autostart          bool              `json:"autostart"`
	IDMapType          string            `json:"idmap_type"`
	CapabilitiesPolicy string            `json:"capabilities_policy"`
	InitEnv            map[string]string `json:"initenv"`
}

type nestedContainerRunner struct {
	ToolURL      string `json:"tool_url"`
	ToolFilename string `json:"tool_filename"`
	ToolSHA256   string `json:"tool_sha256"`
}

func TestExportNestedContainerPreB4Fixture(t *testing.T) {
	out := strings.TrimSpace(os.Getenv("GARM_CONTAINER_FIXTURE_OUT"))
	if out == "" {
		t.Skip("GARM_CONTAINER_FIXTURE_OUT is not set")
	}
	producer := strings.TrimSpace(os.Getenv("GARM_CONTAINER_FIXTURE_SOURCE"))
	if len(producer) != 40 {
		t.Fatalf("GARM_CONTAINER_FIXTURE_SOURCE must be exact 40-char commit, got %q", producer)
	}

	matrix, err := truenasstore.LoadRuntimeBackendMatrix()
	if err != nil {
		t.Fatal(err)
	}
	cell := matrix.Backends["container"].TargetStatus["26.0.0-BETA.3"]
	if cell.Status != "OPEN" || cell.Driver != "container-v1" || cell.ControlSurface != "container.*" {
		t.Fatalf("unexpected BETA.3 container cell: %#v", cell)
	}

	in := provider.Bootstrap{
		Name:        "nested-container-runner",
		OSType:      "linux",
		Arch:        "amd64",
		Flavor:      FlavorLinuxGeneral,
		PoolID:      "nested-container-pool",
		CallbackURL: "https://127.0.0.1:9443/callback",
		MetadataURL: "https://127.0.0.1:9443/metadata",
		Token:       "__RUN_LOCAL_INSTANCE_TOKEN__",
	}
	if err := validateBootstrap(in); err != nil {
		t.Fatal(err)
	}
	desc, err := encodeOwnership(ownership{
		Schema:       "semper-supra.garm-container-owner/1",
		ManagedBy:    "garm-provider-truenas",
		ControllerID: "nested-container-controller",
		PoolID:       in.PoolID,
		RunnerName:   in.Name,
		Profile:      FlavorLinuxGeneral,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := bootstrapEnv(in)
	scrubbed := cloneEnv(initial)
	delete(scrubbed, "GARM_INSTANCE_TOKEN")

	fixture := nestedContainerPreB4Fixture{
		Schema:         "semper-supra.garm-provider-truenas-container-pre-b4-fixture/1",
		ProducerSource: producer,
		Authority:      "SemperSupra/garm-provider-truenas-private#43",
		Target: nestedContainerTarget{
			Version:          "26.0.0-BETA.3",
			SystemVersion:    "TrueNAS-26.0.0-BETA.3",
			Driver:           cell.Driver,
			ControlSurface:   cell.ControlSurface,
			MiddlewareCommit: cell.MiddlewareCommit,
			RequiredMethods:  append([]string(nil), cell.RequiredMethods...),
			SourceBlobs:      cell.SourceBlobs,
			Status:           cell.Status,
		},
		Profile:      FlavorLinuxGeneral,
		ImageFamily:  ImageFamily,
		ExpectedName: ownedName("nested-container-controller", in.Name),
		ExpectedOwnership: map[string]string{
			"schema":        "semper-supra.garm-container-owner/1",
			"managed_by":    "garm-provider-truenas",
			"controller_id": "nested-container-controller",
			"pool_id":       in.PoolID,
			"runner_name":   in.Name,
			"profile":       FlavorLinuxGeneral,
		},
		DesiredCreate: nestedContainerDesiredCreate{
			Description:        desc,
			Autostart:          false,
			IDMapType:          "DEFAULT",
			CapabilitiesPolicy: "DEFAULT",
			InitEnv:            initial,
		},
		ExpectedPostStartInitEnv: scrubbed,
		Runner: nestedContainerRunner{
			ToolURL:      provider.RunnerToolURL,
			ToolFilename: provider.RunnerToolFilename,
			ToolSHA256:   provider.RunnerToolSHA256,
		},
		SourceOracles: map[string]bool{
			"exact_version_required":                 true,
			"foreign_ownership_rejected":             true,
			"create_stopped_then_start_once":         true,
			"persisted_bootstrap_token_scrub_required": true,
			"external_restart_forbidden":             true,
			"active_delete_refused":                  true,
			"final_absence_required":                 true,
			"per_runner_memory_isolation_claimed":    false,
			"runtime_admission_claimed":                false,
			"bootstrap_execution_claimed":              false,
			"github_jit_boundary_claimed":              false,
		},
		ClaimBoundary: "Provider-owned BETA.3 container-v1 B0-B3/B5 pre-B4 fixture only. It binds exact target, desired lifecycle, ownership, security defaults, persisted-token scrub intent and retirement semantics. It explicitly does not claim that stock Ubuntu consumes the bootstrap environment, executes the runner bootstrap, reaches the GitHub/JIT boundary, or provides per-runner memory isolation. B4 remains OPEN until a supported init/credential path is independently qualified.",
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
