package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

func TestGARMV011CreateGetListContractWithMockBackend(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)

	bootstrap := validBootstrap()
	input, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	withStdin(t, input)
	t.Setenv("GARM_COMMAND", "CreateInstance")

	var createOut, createErr bytes.Buffer
	if code := runCLI(context.Background(), &createOut, &createErr); code != 0 {
		t.Fatalf("create failed with exit %d: %s", code, createErr.String())
	}
	var created garmParams.ProviderInstance
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ProviderID == "" {
		t.Fatal("provider_id missing")
	}
	if created.OSType != garmParams.Linux || created.OSArch != garmParams.Amd64 {
		t.Fatalf("unexpected platform mapping: %#v", created)
	}

	t.Setenv("GARM_COMMAND", "GetInstance")
	t.Setenv("GARM_INSTANCE_ID", created.ProviderID)
	var getOut, getErr bytes.Buffer
	if code := runCLI(context.Background(), &getOut, &getErr); code != 0 {
		t.Fatalf("get failed with exit %d: %s", code, getErr.String())
	}
	var got garmParams.ProviderInstance
	if err := json.Unmarshal(getOut.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != created.ProviderID {
		t.Fatalf("get mismatch: %q != %q", got.ProviderID, created.ProviderID)
	}

	t.Setenv("GARM_COMMAND", "ListInstances")
	var listOut, listErr bytes.Buffer
	if code := runCLI(context.Background(), &listOut, &listErr); code != 0 {
		t.Fatalf("list failed with exit %d: %s", code, listErr.String())
	}
	var listed []garmParams.ProviderInstance
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ProviderID != created.ProviderID {
		t.Fatalf("unexpected list: %#v", listed)
	}
}

func TestGARMV011DiscoveryAndSchemas(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)

	t.Setenv("GARM_COMMAND", "GetSupportedInterfaceVersions")
	var versionsOut, versionsErr bytes.Buffer
	if code := runCLI(context.Background(), &versionsOut, &versionsErr); code != 0 {
		t.Fatalf("versions failed with exit %d: %s", code, versionsErr.String())
	}
	var versions []string
	if err := json.Unmarshal(versionsOut.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != commonExecution.Version010 || versions[1] != commonExecution.Version011 {
		t.Fatalf("unexpected interface versions: %#v", versions)
	}

	t.Setenv("GARM_COMMAND", "GetConfigJSONSchema")
	var configSchemaOut, configSchemaErr bytes.Buffer
	if code := runCLI(context.Background(), &configSchemaOut, &configSchemaErr); code != 0 {
		t.Fatalf("config schema failed with exit %d: %s", code, configSchemaErr.String())
	}
	assertJSONObject(t, configSchemaOut.Bytes())
	if !bytes.Contains(configSchemaOut.Bytes(), []byte(`"additionalProperties": false`)) {
		t.Fatal("config schema must fail closed on unknown properties")
	}

	t.Setenv("GARM_COMMAND", "GetExtraSpecsJSONSchema")
	var extraSchemaOut, extraSchemaErr bytes.Buffer
	if code := runCLI(context.Background(), &extraSchemaOut, &extraSchemaErr); code != 0 {
		t.Fatalf("extra schema failed with exit %d: %s", code, extraSchemaErr.String())
	}
	assertJSONObject(t, extraSchemaOut.Bytes())
	if !bytes.Contains(extraSchemaOut.Bytes(), []byte(`"maxProperties": 0`)) {
		t.Fatal("extra-spec schema must reject provider-specific properties")
	}
}

func TestGARMV011ValidatePoolInfoRejectsExtraSpecs(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)
	t.Setenv("GARM_COMMAND", "ValidatePoolInfo")

	t.Setenv("GARM_POOL_EXTRASPECS", "{}")
	var goodOut, goodErr bytes.Buffer
	if code := runCLI(context.Background(), &goodOut, &goodErr); code != 0 {
		t.Fatalf("empty extra specs should validate, exit %d: %s", code, goodErr.String())
	}

	t.Setenv("GARM_POOL_EXTRASPECS", `{"surprise":true}`)
	var badOut, badErr bytes.Buffer
	if code := runCLI(context.Background(), &badOut, &badErr); code == 0 {
		t.Fatal("unsupported extra specs unexpectedly validated")
	}
	if !strings.Contains(badErr.String(), "extra specs") {
		t.Fatalf("unexpected validation error: %s", badErr.String())
	}
}

func TestCreateRejectsNonJITBootstrap(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)
	bootstrap := validBootstrap()
	bootstrap.JitConfigEnabled = false
	input, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	withStdin(t, input)
	t.Setenv("GARM_COMMAND", "CreateInstance")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("non-JIT bootstrap unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "JIT runner configuration is required") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func TestCreateRejectsUnsupportedBootstrapMutation(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)
	bootstrap := validBootstrap()
	bootstrap.SSHKeys = []string{"ssh-ed25519 AAAAtest"}
	input, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	withStdin(t, input)
	t.Setenv("GARM_COMMAND", "CreateInstance")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("unsupported SSH injection unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "SSH key injection") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func TestV010CompatibilityGetVersion(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)
	t.Setenv("GARM_INTERFACE_VERSION", "")
	t.Setenv("GARM_COMMAND", "GetVersion")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code != 0 {
		t.Fatalf("v0.1.0 GetVersion failed with exit %d: %s", code, stderr.String())
	}
	if stdout.String() != Version {
		t.Fatalf("unexpected version output %q", stdout.String())
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
	writeConfig(t, cfgPath, cfg)
	setBaseEnvironment(t, cfgPath)
	t.Setenv("TEST_TRUENAS_API_KEY", "")
	t.Setenv("GARM_COMMAND", "ListInstances")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("missing runtime API key unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "TEST_TRUENAS_API_KEY") {
		t.Fatalf("missing runtime API key should fail before network, got %s", stderr.String())
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
	writeConfig(t, cfgPath, cfg)
	setBaseEnvironment(t, cfgPath)
	t.Setenv("TEST_TRUENAS_API_KEY", "test-only-key")
	t.Setenv("GARM_COMMAND", "GetVersion")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("insecure TLS unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "insecure TLS") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func TestUnknownModeFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	if err := os.WriteFile(cfgPath, []byte(`{"mode":"live"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	setBaseEnvironment(t, cfgPath)
	t.Setenv("GARM_COMMAND", "GetVersion")

	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("unknown mode unexpectedly succeeded")
	}
}

func TestConfigRejectsUnknownFields(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	if err := os.WriteFile(cfgPath, []byte(`{"mode":"mock","state_file":"state.json","surprise":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(cfgPath); err == nil {
		t.Fatal("unknown provider config field unexpectedly accepted")
	}
}

func validBootstrap() garmParams.BootstrapInstance {
	return garmParams.BootstrapInstance{
		Name:             "runner-123",
		RepoURL:          "https://github.com/SemperSupra/example",
		CallbackURL:      "https://garm.example.invalid/api/v1/callbacks",
		MetadataURL:      "https://garm.example.invalid/api/v1/metadata",
		InstanceToken:    "test-token",
		OSArch:           garmParams.Amd64,
		OSType:           garmParams.Linux,
		Flavor:           provider.FlavorLinuxGeneral,
		Image:            provider.RunnerImage,
		Labels:           []string{"self-hosted", "linux", "x64"},
		PoolID:           "pool-123",
		ExtraSpecs:       json.RawMessage(`{}`),
		JitConfigEnabled: true,
	}
}

func writeMockConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "provider.json")
	statePath := filepath.Join(tmp, "state.json")
	writeConfig(t, cfgPath, config{Mode: "mock", StateFile: statePath})
	return cfgPath
}

func writeConfig(t *testing.T, path string, cfg config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setBaseEnvironment(t *testing.T, cfgPath string) {
	t.Helper()
	t.Setenv("GARM_PROVIDER_CONFIG_FILE", cfgPath)
	t.Setenv("GARM_CONTROLLER_ID", "controller-123")
	t.Setenv("GARM_INTERFACE_VERSION", commonExecution.Version011)
	t.Setenv("GARM_POOL_ID", "pool-123")
	t.Setenv("GARM_INSTANCE_ID", "")
	t.Setenv("GARM_POOL_EXTRASPECS", "")
}

func withStdin(t *testing.T, data []byte) {
	t.Helper()
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	})
}

func assertJSONObject(t *testing.T, data []byte) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("invalid JSON object: %v\n%s", err, string(data))
	}
	if len(obj) == 0 {
		t.Fatal("JSON object unexpectedly empty")
	}
}
