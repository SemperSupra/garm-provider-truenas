package truenasstore

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

const runtimeBackendTargetMatrixFixture = "testdata/runtime-backend-target-matrix.json"

type runtimeBackendCell struct {
	Status           string            `json:"status"`
	Driver           string            `json:"driver"`
	ControlSurface   string            `json:"control_surface"`
	MiddlewareCommit string            `json:"middleware_commit"`
	SourceBlobs      map[string]string `json:"source_blobs"`
}

type runtimeBackendEntry struct {
	ConfigID             string                        `json:"config_id"`
	ProviderName         string                        `json:"provider_name"`
	Profile              string                        `json:"profile"`
	ImplementationStatus string                        `json:"implementation_status"`
	TargetStatus         map[string]runtimeBackendCell `json:"target_status"`
}

type runtimeBackendTargetMatrix struct {
	Schema                           string                         `json:"schema"`
	Authority                        string                         `json:"authority"`
	RuntimeInheritanceAllowed        bool                           `json:"runtime_inheritance_allowed"`
	RequiredTargetVersions           []string                       `json:"required_target_versions"`
	Backends                         map[string]runtimeBackendEntry `json:"backends"`
	AllAdmittedRuntimeCellsQualified bool                           `json:"all_admitted_runtime_cells_qualified"`
}

func loadRuntimeBackendTargetMatrix(t *testing.T) runtimeBackendTargetMatrix {
	t.Helper()
	raw, err := os.ReadFile(runtimeBackendTargetMatrixFixture)
	if err != nil {
		t.Fatal(err)
	}
	var matrix runtimeBackendTargetMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	return matrix
}

func TestRuntimeBackendTargetMatrixContract(t *testing.T) {
	matrix := loadRuntimeBackendTargetMatrix(t)
	if matrix.Schema != "semper-supra.garm-provider-truenas-runtime-backend-target-matrix/1" {
		t.Fatalf("unexpected runtime backend matrix schema: %q", matrix.Schema)
	}
	if matrix.Authority != "SemperSupra/garm-provider-truenas-private#43" {
		t.Fatalf("unexpected runtime backend authority: %q", matrix.Authority)
	}
	if matrix.RuntimeInheritanceAllowed {
		t.Fatal("runtime support must never inherit between exact TrueNAS versions")
	}
	if matrix.AllAdmittedRuntimeCellsQualified {
		t.Fatal("matrix must remain fail-closed while admitted Container/VM cells are OPEN")
	}

	g5 := loadG5VersionMatrixRaw(t)
	rawTargets, ok := g5["targets"].([]any)
	if !ok {
		t.Fatal("G5 targets missing")
	}
	g5Targets := make([]string, 0, len(rawTargets))
	g5Commits := map[string]string{}
	for _, raw := range rawTargets {
		row := raw.(map[string]any)
		version := row["version"].(string)
		g5Targets = append(g5Targets, version)
		g5Commits[version] = row["middleware_commit"].(string)
	}
	sort.Strings(g5Targets)
	gotTargets := append([]string(nil), matrix.RequiredTargetVersions...)
	sort.Strings(gotTargets)
	if !reflect.DeepEqual(gotTargets, g5Targets) {
		t.Fatalf("runtime backend targets drifted from G5 exact targets: got %#v want %#v", gotTargets, g5Targets)
	}

	requiredBackends := map[string]struct {
		configID string
		provider string
		profile  string
	}{
		"apps":      {"apps", "truenas-apps", "truenas-linux-general"},
		"container": {"container", "truenas-containers", "truenas-container-linux-general"},
		"vm":        {"vm", "truenas-vms", "truenas-vm-linux-general"},
	}
	if len(matrix.Backends) != len(requiredBackends) {
		t.Fatalf("expected exactly %d backend families, got %d", len(requiredBackends), len(matrix.Backends))
	}
	allowedStatus := map[string]bool{"PASS": true, "OPEN": true, "FAIL": true, "NOT_ADMITTED": true, "NOT_APPLICABLE": true}
	for name, want := range requiredBackends {
		backend, ok := matrix.Backends[name]
		if !ok {
			t.Fatalf("missing backend %q", name)
		}
		if backend.ConfigID != want.configID || backend.ProviderName != want.provider || backend.Profile != want.profile {
			t.Fatalf("backend %s identity drifted: %#v", name, backend)
		}
		if len(backend.TargetStatus) != len(matrix.RequiredTargetVersions) {
			t.Fatalf("backend %s does not cover every exact target", name)
		}
		for _, version := range matrix.RequiredTargetVersions {
			cell, ok := backend.TargetStatus[version]
			if !ok {
				t.Fatalf("backend %s missing exact target %s", name, version)
			}
			if !allowedStatus[cell.Status] {
				t.Fatalf("backend %s target %s has unsupported status %q", name, version, cell.Status)
			}
			if cell.MiddlewareCommit != g5Commits[version] {
				t.Fatalf("backend %s target %s middleware commit drifted: %s != %s", name, version, cell.MiddlewareCommit, g5Commits[version])
			}
			if cell.Driver == "" || cell.ControlSurface == "" || len(cell.SourceBlobs) == 0 {
				t.Fatalf("backend %s target %s lacks source-bound driver metadata", name, version)
			}
		}
	}

	for version, cell := range matrix.Backends["apps"].TargetStatus {
		if cell.Status != "PASS" {
			t.Fatalf("existing Apps G5 row %s must remain PASS, got %s", version, cell.Status)
		}
		if cell.Driver != "apps-v1" || cell.ControlSurface != "app.*" {
			t.Fatalf("Apps driver drifted at %s: %#v", version, cell)
		}
	}

	for _, version := range []string{"25.04.1", "25.04.2.6", "25.10.7"} {
		cell := matrix.Backends["container"].TargetStatus[version]
		if cell.Status != "OPEN" || cell.Driver != "virt-instance-container-v1" || cell.ControlSurface != "virt.instance" {
			t.Fatalf("25.x Container driver contract drifted at %s: %#v", version, cell)
		}
	}
	c26 := matrix.Backends["container"].TargetStatus["26.0.0-BETA.3"]
	if c26.Status != "OPEN" || c26.Driver != "container-v1" || c26.ControlSurface != "container.*" {
		t.Fatalf("26 Container driver contract drifted: %#v", c26)
	}

	vm25041 := matrix.Backends["vm"].TargetStatus["25.04.1"]
	if vm25041.Status != "NOT_ADMITTED" || vm25041.Driver != "virt-instance-vm-v1" {
		t.Fatalf("25.04.1 VM must remain explicitly not admitted: %#v", vm25041)
	}
	for _, version := range []string{"25.04.2.6", "25.10.7", "26.0.0-BETA.3"} {
		cell := matrix.Backends["vm"].TargetStatus[version]
		if cell.Status != "OPEN" || cell.Driver != "vm-v1" || cell.ControlSurface != "vm.*" {
			t.Fatalf("classic VM driver contract drifted at %s: %#v", version, cell)
		}
	}
}
