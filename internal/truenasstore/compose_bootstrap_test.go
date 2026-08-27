package truenasstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestComposeWiresContainerBootstrapAndVolatileRunnerHome(t *testing.T) {
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
	command, _ := runner["command"].(string)
	if command != containerBootstrapCommand() {
		t.Fatal("runner command is not the container-native bootstrap")
	}
	if strings.Contains(command, spec.BootstrapToken) {
		t.Fatal("bootstrap token was interpolated into the runner command")
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
	want := []string{
		"/home/runner/actions-runner:rw,nosuid,nodev,uid=1001,gid=1001,mode=0700",
		"/home/runner/_work:rw,nosuid,nodev,uid=1001,gid=1001,mode=0700",
	}
	if !reflect.DeepEqual(tmpfs, want) {
		t.Fatalf("unexpected tmpfs profile: %#v", tmpfs)
	}
	for _, mount := range tmpfs {
		if strings.Contains(mount, "noexec") {
			t.Fatalf("Actions work/runner filesystem must remain executable: %s", mount)
		}
	}
}
