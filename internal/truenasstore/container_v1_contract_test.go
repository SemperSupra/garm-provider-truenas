package truenasstore

import (
	"strings"
	"testing"
)

func TestContainerV1Beta3SourceContractIsExplicitAndStillOpen(t *testing.T) {
	matrix := loadRuntimeBackendTargetMatrix(t)
	cell := matrix.Backends["container"].TargetStatus["26.0.0-BETA.3"]

	if cell.Status != "OPEN" {
		t.Fatalf("container-v1 must remain OPEN before runtime evidence, got %q", cell.Status)
	}
	if cell.Driver != "container-v1" || cell.ControlSurface != "container.*" {
		t.Fatalf("unexpected container-v1 driver contract: %#v", cell)
	}
	if got := cell.SourceBlobs["src/middlewared/middlewared/api/v26_0_0/container.py"]; got != "6c1d11b9d42d5bfc6dd242d77c017b7fbe1a26aa" {
		t.Fatalf("container API schema blob drifted: %q", got)
	}

	required := map[string]bool{}
	for _, method := range cell.RequiredMethods {
		required[method] = true
	}
	for _, method := range []string{
		"system.version",
		"container.query",
		"container.create",
		"container.update",
		"container.delete",
		"container.start",
		"container.stop",
		"container.image.query_registry",
		"pool.dataset.query",
		"filesystem.put",
		"filesystem.stat",
	} {
		if !required[method] {
			t.Fatalf("container-v1 source contract is missing %s", method)
		}
	}

	for path, want := range map[string]string{
		"src/middlewared/middlewared/api/v26_0_0/filesystem.py": "50a1a4432b35149d1f6c38230e31a95f1ab0bf0a",
		"src/middlewared/middlewared/plugins/filesystem.py": "d8feb5e7d2d0565ad7ffe1ae79466bb072911541",
		"src/middlewared/middlewared/api/v26_0_0/pool_dataset.py": "a32b96aff2e63d780e3cf3786dca34cf06fd2468",
		"src/middlewared/middlewared/plugins/pool_/dataset.py": "2bb0c179e0ee54e1cbdd5e106d31b81469d4218d",
	} {
		if got := cell.SourceBlobs[path]; got != want {
			t.Fatalf("container bootstrap source drift at %s: got %q want %q", path, got, want)
		}
	}
	if !strings.Contains(cell.Note, "temporary init command + one-time initenv") {
		t.Fatalf("supported bootstrap lowering disappeared: %q", cell.Note)
	}
	if !strings.Contains(cell.Note, "DEFAULT idmap keeps container root host-unprivileged") {
		t.Fatalf("container-root qualification boundary disappeared: %q", cell.Note)
	}
	if !strings.Contains(cell.Note, "No Apps-style per-container memory ceiling") {
		t.Fatalf("container-v1 resource-isolation non-claim disappeared: %q", cell.Note)
	}
}

func TestContainerV1DoesNotBorrowAppsAdmission(t *testing.T) {
	_, cell, err := ResolveRuntimeBackend("container", "TrueNAS-26.0.0-BETA.3")
	if err != nil {
		t.Fatal(err)
	}
	if RuntimeCellOperationallyAdmitted(cell) {
		t.Fatal("source-complete container-v1 must not inherit Apps runtime admission")
	}
}
