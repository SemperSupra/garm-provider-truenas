package truenasstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestComposeWiresContainerBootstrapAndCredentialTmpfs(t *testing.T) {
	spec := fixedSpec()
	compose, err := composeConfig(spec)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(compose), &doc); err != nil {
		t.Fatal(err)
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		t.Fatal("services object missing")
	}
	runner, ok := services["runner"].(map[string]any)
	if !ok {
		t.Fatal("runner service missing")
	}

	rawEntrypoint, ok := runner["entrypoint"].([]any)
	if !ok || len(rawEntrypoint) != 3 {
		t.Fatalf("runner entrypoint is not the deterministic shell bootstrap: %#v", runner["entrypoint"])
	}
	entrypoint := make([]string, 0, len(rawEntrypoint))
	for _, raw := range rawEntrypoint {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("entrypoint value is not a string: %#v", raw)
		}
		entrypoint = append(entrypoint, value)
	}
	wantEntrypoint := []string{"/bin/sh", "-c", containerBootstrapCommand()}
	if !reflect.DeepEqual(entrypoint, wantEntrypoint) {
		t.Fatalf("unexpected bootstrap entrypoint: %#v", entrypoint)
	}
	if strings.Contains(entrypoint[2], spec.BootstrapToken) {
		t.Fatal("bootstrap token was interpolated into the entrypoint script")
	}
	if _, ok := runner["command"]; ok {
		t.Fatal("runner command must not override the deterministic bootstrap entrypoint")
	}

	environment, ok := runner["environment"].(map[string]any)
	if !ok {
		t.Fatal("runner environment missing")
	}
	if environment["GARM_INSTANCE_TOKEN"] != spec.BootstrapToken {
		t.Fatal("bootstrap token is not passed through the expected isolated environment field")
	}
	if environment["GARM_CALLBACK_URL"] != spec.CallbackURL || environment["GARM_METADATA_URL"] != spec.MetadataURL {
		t.Fatalf("GARM bootstrap endpoints missing: %#v", environment)
	}
	if environment["GARM_RUNNER_DOWNLOAD_URL"] != spec.RunnerDownloadURL ||
		environment["GARM_RUNNER_FILENAME"] != spec.RunnerFilename ||
		environment["GARM_RUNNER_SHA256"] != spec.RunnerSHA256 {
		t.Fatalf("verified runner tool contract missing from environment: %#v", environment)
	}

	rawTmpfs, ok := runner["tmpfs"].([]any)
	if !ok {
		t.Fatalf("tmpfs is not a list: %#v", runner["tmpfs"])
	}
	tmpfs := make([]string, 0, len(rawTmpfs))
	for _, raw := range rawTmpfs {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("tmpfs entry is not a string: %#v", raw)
		}
		tmpfs = append(tmpfs, value)
	}
	wantTmpfs := []string{
		"/run/garm-jit:rw,nosuid,nodev,noexec,uid=1001,gid=1001,mode=0700",
	}
	if !reflect.DeepEqual(tmpfs, wantTmpfs) {
		t.Fatalf("unexpected tmpfs profile: %#v", tmpfs)
	}
	if !strings.Contains(tmpfs[0], "noexec") {
		t.Fatalf("credential tmpfs must be non-executable: %s", tmpfs[0])
	}
	for _, forbidden := range []string{"/home/runner/actions-runner", "/home/runner/_work"} {
		if strings.Contains(tmpfs[0], forbidden) {
			t.Fatalf("runner/work tree must remain on the executable container layer: %s", tmpfs[0])
		}
	}
}
