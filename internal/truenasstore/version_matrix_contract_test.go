package truenasstore

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

const g5VersionMatrixFixture = "testdata/truenas-version-matrix.json"

func loadG5VersionMatrixRaw(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(g5VersionMatrixFixture)
	if err != nil {
		t.Fatal(err)
	}
	var matrix map[string]any
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	return matrix
}

func stringField(t *testing.T, obj map[string]any, key string) string {
	t.Helper()
	value, ok := obj[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing string field %q in %#v", key, obj)
	}
	return value
}

func objectField(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := obj[key].(map[string]any)
	if !ok {
		t.Fatalf("missing object field %q in %#v", key, obj)
	}
	return value
}

func TestG5VersionMatrixContract(t *testing.T) {
	matrix := loadG5VersionMatrixRaw(t)
	if got := stringField(t, matrix, "schema"); got != "semper-supra.garm-provider-truenas-g5-version-matrix/1" {
		t.Fatalf("unexpected G5 matrix schema: %q", got)
	}
	if got := stringField(t, matrix, "provider_product_source"); got != "14535745dc3aa3c0b5466da7c704bca4d23dcec5" {
		t.Fatalf("unexpected provider product source: %q", got)
	}
	if value, ok := matrix["runtime_inheritance_allowed"].(bool); !ok || value {
		t.Fatal("source equivalence must never imply runtime qualification inheritance")
	}

	methodsRaw, ok := matrix["required_method_surfaces"].([]any)
	if !ok {
		t.Fatal("required_method_surfaces must be an array")
	}
	methods := make([]string, 0, len(methodsRaw))
	for _, value := range methodsRaw {
		method, ok := value.(string)
		if !ok {
			t.Fatal("required_method_surfaces contains a non-string")
		}
		methods = append(methods, method)
	}
	wantMethods := []string{
		"system.version",
		"system.general.config",
		"system.general.update",
		"system.general.ui_restart",
		"api_key.create",
		"api_key.delete",
		"certificate.create",
		"certificate.delete",
		"app.create",
		"app.query",
		"app.config",
		"app.stop",
		"app.delete",
	}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("required method surface drifted: %#v", methods)
	}

	wantCommits := map[string]string{
		"25.04.1":       "74ab5a2d373be4097dece257d00e1086376333ba",
		"25.04.2.6":     "244b717370fe35fb9eefc3096acfbc25f12b57b6",
		"25.10.7":       "8ede398839710e56893d88ce85088139d8fab18e",
		"26.0.0-BETA.3": "81e1265a86083888ba94a2bdfc02ff5c9c5ef6a3",
	}
	targetsRaw, ok := matrix["targets"].([]any)
	if !ok || len(targetsRaw) != len(wantCommits) {
		t.Fatalf("expected %d exact targets", len(wantCommits))
	}
	type targetInfo struct {
		commit string
		group  string
		blobs  map[string]any
	}
	byVersion := map[string]targetInfo{}
	for _, rawTarget := range targetsRaw {
		target, ok := rawTarget.(map[string]any)
		if !ok {
			t.Fatal("target row is not an object")
		}
		version := stringField(t, target, "version")
		want, ok := wantCommits[version]
		if !ok {
			t.Fatalf("unexpected target version %q", version)
		}
		commit := stringField(t, target, "middleware_commit")
		if commit != want {
			t.Fatalf("%s middleware commit drifted: got %s want %s", version, commit, want)
		}
		if got := stringField(t, target, "system_version"); got != "TrueNAS-"+version {
			t.Fatalf("%s system version drifted: %q", version, got)
		}
		group := stringField(t, target, "source_equivalence_group")
		blobs := objectField(t, target, "source_blobs")
		if len(blobs) < 7 {
			t.Fatalf("%s source matrix incomplete", version)
		}
		if _, exists := byVersion[version]; exists {
			t.Fatalf("duplicate target version %q", version)
		}
		byVersion[version] = targetInfo{commit: commit, group: group, blobs: blobs}
	}

	a := byVersion["25.04.1"]
	b := byVersion["25.04.2.6"]
	if a.group != b.group {
		t.Fatalf("25.04 maintenance targets should share source-equivalence group: %q vs %q", a.group, b.group)
	}
	for _, path := range []string{
		"src/middlewared/middlewared/plugins/apps/crud.py",
		"src/middlewared/middlewared/plugins/apps/ix_apps/query.py",
		"src/middlewared/middlewared/plugins/api_key.py",
		"src/middlewared/middlewared/plugins/system_general/update.py",
		"src/middlewared/middlewared/plugins/system_general/ui.py",
		"src/middlewared/middlewared/plugins/filesystem.py",
	} {
		if a.blobs[path] != b.blobs[path] {
			t.Fatalf("expected 25.04 source equivalence at %s: %v != %v", path, a.blobs[path], b.blobs[path])
		}
	}
	certPath := "src/middlewared/middlewared/plugins/crypto_/certificates.py"
	if a.blobs[certPath] == b.blobs[certPath] {
		t.Fatal("25.04.1 and 25.04.2.6 certificate blobs must remain explicitly distinct")
	}

	existing := loadIXAppsContract(t)
	if existing.Middleware.Commit != a.commit {
		t.Fatalf("existing 25.04.1 contract commit drifted from G5 matrix: %s != %s", existing.Middleware.Commit, a.commit)
	}
	for path, sha := range existing.Middleware.SourceBlobs {
		if got, ok := a.blobs[path]; ok && got != sha {
			t.Fatalf("existing 25.04.1 source blob drifted at %s: %v != %s", path, got, sha)
		}
	}

	groups := []string{
		byVersion["25.04.1"].group,
		byVersion["25.10.7"].group,
		byVersion["26.0.0-BETA.3"].group,
	}
	sort.Strings(groups)
	for i := 1; i < len(groups); i++ {
		if groups[i] == groups[i-1] {
			t.Fatalf("independent release families unexpectedly share equivalence group %q", groups[i])
		}
	}
}
