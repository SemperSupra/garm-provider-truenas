package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

func TestCreateGetListContractWithMockBackend(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	statePath := filepath.Join(tmp, "state.json")
	cfg := config{Mode: "mock", StateFile: statePath}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_POOL_ID", "pool-123")

	bootstrap := provider.Bootstrap{
		Name:        "runner-123",
		OSType:      "linux",
		Arch:        "amd64",
		Flavor:      provider.FlavorLinuxGeneral,
		PoolID:      "pool-123",
		CallbackURL: "https://garm.example.invalid/api/v1/callbacks",
		MetadataURL: "https://garm.example.invalid/api/v1/metadata",
		Token:       "test-token",
	}
	input, _ := json.Marshal(bootstrap)

	t.Setenv("GARM_COMMAND", "CreateInstance")
	var createOut bytes.Buffer
	if err := run(context.Background(), bytes.NewReader(input), &createOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var created provider.Instance
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ProviderID == "" {
		t.Fatal("provider_id missing")
	}

	t.Setenv("GARM_COMMAND", "GetInstance")
	t.Setenv("GARM_INSTANCE_ID", created.ProviderID)
	var getOut bytes.Buffer
	if err := run(context.Background(), bytes.NewReader(nil), &getOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got provider.Instance
	if err := json.Unmarshal(getOut.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != created.ProviderID {
		t.Fatalf("get mismatch: %q != %q", got.ProviderID, created.ProviderID)
	}

	t.Setenv("GARM_COMMAND", "ListInstances")
	var listOut bytes.Buffer
	if err := run(context.Background(), bytes.NewReader(nil), &listOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var listed []provider.Instance
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ProviderID != created.ProviderID {
		t.Fatalf("unexpected list: %#v", listed)
	}
}

func TestLiveModeFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	if err := os.WriteFile(cfgPath, []byte("{\"mode\":\"live\",\"state_file\":\"unused\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_COMMAND", "ListInstances")
	if err := run(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("live mode unexpectedly succeeded")
	}
}
