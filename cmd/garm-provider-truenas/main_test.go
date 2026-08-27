package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestTrueNASModeRequiresRuntimeAPIKeyBeforeNetwork(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	cfg := config{
		Mode: "truenas",
		TrueNAS: &trueNASConfig{
			Host:      "truenas.example.invalid",
			Username:  "automation",
			APIKeyEnv: "TEST_TRUENAS_API_KEY",
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TRUENAS_API_KEY", "")
	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_COMMAND", "ListInstances")

	err := run(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "TEST_TRUENAS_API_KEY") {
		t.Fatalf("missing runtime API key should fail before network, got %v", err)
	}
}

func TestTrueNASModeRejectsInsecureTLSBeforeNetwork(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	cfg := config{
		Mode: "truenas",
		TrueNAS: &trueNASConfig{
			Host:               "truenas.example.invalid",
			Username:           "automation",
			APIKeyEnv:          "TEST_TRUENAS_API_KEY",
			InsecureSkipVerify: true,
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TRUENAS_API_KEY", "test-only-key")
	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_COMMAND", "ListInstances")

	err := run(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "insecure TLS") {
		t.Fatalf("insecure TLS should fail before network, got %v", err)
	}
}

func TestUnknownModeFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	if err := os.WriteFile(cfgPath, []byte("{\"mode\":\"live\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_COMMAND", "ListInstances")
	if err := run(context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown mode unexpectedly succeeded")
	}
}

func TestConfigRejectsUnknownFields(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	if err := os.WriteFile(cfgPath, []byte("{\"mode\":\"mock\",\"state_file\":\"state.json\",\"surprise\":true}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(cfgPath); err == nil {
		t.Fatal("unknown provider config field unexpectedly accepted")
	}
}
