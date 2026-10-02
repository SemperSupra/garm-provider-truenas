package truenasstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

const (
	g3ControllerID = "g3-controller"
	g3PoolID       = "g3-pool"
)

type g3CaptureClient struct {
	app *provider.App
}

func (c *g3CaptureClient) CreateApp(_ context.Context, spec provider.AppSpec) (provider.App, error) {
	app := provider.App{Spec: spec, State: provider.StateRunning}
	c.app = &app
	return app, nil
}

func (c *g3CaptureClient) GetApp(_ context.Context, name string) (provider.App, error) {
	if c.app == nil || c.app.Spec.Name != name {
		return provider.App{}, provider.ErrNotFound
	}
	return *c.app, nil
}

func (c *g3CaptureClient) ListApps(context.Context) ([]provider.App, error) {
	if c.app == nil {
		return nil, nil
	}
	return []provider.App{*c.app}, nil
}

func (c *g3CaptureClient) DeleteApp(_ context.Context, name string) error {
	if c.app != nil && c.app.Spec.Name == name {
		c.app = nil
	}
	return nil
}

type g3Bundle struct {
	Schema                string         `json:"schema"`
	ProviderProductSource string         `json:"provider_product_source"`
	ProducerSource        string         `json:"producer_source"`
	ControllerID          string         `json:"controller_id"`
	PoolID                string         `json:"pool_id"`
	BootstrapTemplate     map[string]any `json:"bootstrap_template"`
	ExpectedAppName       string         `json:"expected_app_name"`
	ExpectedCompose       map[string]any `json:"expected_compose_template"`
	Runner                map[string]any `json:"runner"`
	RunLocalSubstitutions []string       `json:"run_local_substitutions"`
	Claims                []string       `json:"claims"`
	NonClaims             []string       `json:"non_claims"`
}

func TestExportNestedG3CreateFixtureBundle(t *testing.T) {
	out := os.Getenv("G3_FIXTURE_OUT")
	if out == "" {
		t.Skip("G3_FIXTURE_OUT not set")
	}

	osName := "linux"
	arch := "x64"
	filename := provider.RunnerToolFilename
	downloadURL := provider.RunnerToolURL
	checksum := provider.RunnerToolSHA256

	bootstrap := garmParams.BootstrapInstance{
		Name: "g3-synthetic-runner",
		Tools: []garmParams.RunnerApplicationDownload{{
			OS:             &osName,
			Architecture:   &arch,
			DownloadURL:    &downloadURL,
			Filename:       &filename,
			SHA256Checksum: &checksum,
		}},
		RepoURL:       "https://github.com/SemperSupra/garm-provider-truenas",
		CallbackURL:   "https://g3-callback.invalid/api/v1/callbacks",
		MetadataURL:   "https://g3-callback.invalid/api/v1/metadata",
		InstanceToken: "G3_INSTANCE_TOKEN_PLACEHOLDER",
		ExtraSpecs: json.RawMessage(
			`{"runner_install_template":"G3_RUNNER_INSTALL_TEMPLATE_COMPAT_PLACEHOLDER"}`,
		),
		OSArch:           garmParams.Amd64,
		OSType:           garmParams.Linux,
		Flavor:           provider.FlavorLinuxGeneral,
		Image:            provider.RunnerImage,
		Labels:           []string{"self-hosted", "linux", "x64", "g3-synthetic"},
		PoolID:           g3PoolID,
		JitConfigEnabled: true,
	}

	bootstrapRaw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var bootstrapDoc map[string]any
	if err := json.Unmarshal(bootstrapRaw, &bootstrapDoc); err != nil {
		t.Fatal(err)
	}

	client := &g3CaptureClient{}
	manager, err := provider.NewManager(client, g3ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(context.Background(), provider.Bootstrap{
		Name:        bootstrap.Name,
		OSType:      string(bootstrap.OSType),
		Arch:        string(bootstrap.OSArch),
		Flavor:      bootstrap.Flavor,
		PoolID:      bootstrap.PoolID,
		CallbackURL: bootstrap.CallbackURL,
		MetadataURL: bootstrap.MetadataURL,
		Token:       bootstrap.InstanceToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.app == nil {
		t.Fatal("manager did not materialize an AppSpec")
	}

	composeRaw, err := composeConfig(client.app.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var composeDoc map[string]any
	if err := json.Unmarshal([]byte(composeRaw), &composeDoc); err != nil {
		t.Fatal(err)
	}

	producerSource := os.Getenv("G3_PRODUCER_SOURCE")
	if producerSource == "" {
		producerSource = "unknown"
	}

	bundle := g3Bundle{
		Schema:                "semper-supra.garm-provider-truenas-g3-create-fixture/1",
		ProviderProductSource: "14535745dc3aa3c0b5466da7c704bca4d23dcec5",
		ProducerSource:        producerSource,
		ControllerID:          g3ControllerID,
		PoolID:                g3PoolID,
		BootstrapTemplate:     bootstrapDoc,
		ExpectedAppName:       created.ProviderID,
		ExpectedCompose:       composeDoc,
		Runner: map[string]any{
			"image":             provider.RunnerImage,
			"version":           provider.RunnerVersion,
			"tool_url":          provider.RunnerToolURL,
			"tool_filename":     provider.RunnerToolFilename,
			"tool_sha256":       provider.RunnerToolSHA256,
			"cpu":               provider.GeneralCPU,
			"memory_bytes":      provider.GeneralMemoryBytes,
			"execution_profile": provider.FlavorLinuxGeneral,
		},
		RunLocalSubstitutions: []string{
			"bootstrap_template.callback-url",
			"bootstrap_template.metadata-url",
			"bootstrap_template.instance-token",
			"expected_compose_template.services.runner.environment.GARM_CALLBACK_URL",
			"expected_compose_template.services.runner.environment.GARM_METADATA_URL",
			"expected_compose_template.services.runner.environment.GARM_INSTANCE_TOKEN",
		},
		Claims: []string{
			"bootstrap template uses the exact GARM provider-common JSON contract",
			"expected App name is produced by exact provider Manager.Create",
			"expected Compose is produced by exact provider composeConfig",
			"only callback URL, metadata URL, and synthetic instance token may be lowered per run",
		},
		NonClaims: []string{
			"no TrueNAS runtime realization",
			"no GitHub JIT registration",
			"no private repository authority",
			"no physical-host qualification",
			"no capacity promotion",
		},
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
