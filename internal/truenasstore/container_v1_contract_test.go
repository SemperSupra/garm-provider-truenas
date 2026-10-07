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
	} {
		if !required[method] {
			t.Fatalf("container-v1 source contract is missing %s", method)
		}
	}

	if !strings.Contains(cell.Note, "does not expose an Apps-style per-container memory ceiling") {
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
