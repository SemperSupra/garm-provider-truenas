package truenasstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

const (
	g4ControllerID = "g4-controller"
	g4PoolID       = "g4-pool"
)

type g4CaptureClient struct {
	apps map[string]provider.App
}

func newG4CaptureClient() *g4CaptureClient {
	return &g4CaptureClient{apps: map[string]provider.App{}}
}

func (c *g4CaptureClient) CreateApp(_ context.Context, spec provider.AppSpec) (provider.App, error) {
	app := provider.App{Spec: spec, State: provider.StateRunning}
	c.apps[spec.Name] = app
	return app, nil
}

func (c *g4CaptureClient) GetApp(_ context.Context, name string) (provider.App, error) {
	app, ok := c.apps[name]
	if !ok {
		return provider.App{}, provider.ErrNotFound
	}
	return app, nil
}

func (c *g4CaptureClient) ListApps(context.Context) ([]provider.App, error) {
	out := make([]provider.App, 0, len(c.apps))
	for _, app := range c.apps {
		out = append(out, app)
	}
	return out, nil
}

func (c *g4CaptureClient) DeleteApp(_ context.Context, name string) error {
	delete(c.apps, name)
	return nil
}

type g4RunnerFixture struct {
	Slot              string         `json:"slot"`
	BootstrapTemplate map[string]any `json:"bootstrap_template"`
	ExpectedAppName   string         `json:"expected_app_name"`
	ExpectedCompose   map[string]any `json:"expected_compose_template"`
}

type g4Bundle struct {
	Schema                string            `json:"schema"`
	ProviderProductSource string            `json:"provider_product_source"`
	ProducerSource        string            `json:"producer_source"`
	ControllerID          string            `json:"controller_id"`
	PoolID                string            `json:"pool_id"`
	Runners               []g4RunnerFixture `json:"runners"`
	Runner                map[string]any    `json:"runner"`
	RetirementOrders      [][]string        `json:"retirement_orders"`
	RunLocalSubstitutions []string          `json:"run_local_substitutions"`
	SourceOracles         map[string]bool   `json:"source_oracles"`
	Claims                []string          `json:"claims"`
	NonClaims             []string          `json:"non_claims"`
}

func g4Bootstrap(slot string) garmParams.BootstrapInstance {
	osName := "linux"
	arch := "x64"
	filename := provider.RunnerToolFilename
	downloadURL := provider.RunnerToolURL
	checksum := provider.RunnerToolSHA256
	requestedName := "g4-synthetic-runner-" + slot + "-" + strings.Repeat(slot[:1], 32)
	upper := strings.ToUpper(slot)

	return garmParams.BootstrapInstance{
		Name: requestedName,
		Tools: []garmParams.RunnerApplicationDownload{{
			OS:             &osName,
			Architecture:   &arch,
			DownloadURL:    &downloadURL,
			Filename:       &filename,
			SHA256Checksum: &checksum,
		}},
		RepoURL:       "https://github.com/SemperSupra/garm-provider-truenas",
		CallbackURL:   "https://g4-callback.invalid/api/v1/" + slot + "/callbacks",
		MetadataURL:   "https://g4-callback.invalid/api/v1/" + slot + "/metadata",
		InstanceToken: "G4_" + upper + "_INSTANCE_TOKEN_PLACEHOLDER",
		ExtraSpecs: json.RawMessage(
			`{"runner_install_template":"G4_RUNNER_INSTALL_TEMPLATE_COMPAT_PLACEHOLDER"}`,
		),
		OSArch:           garmParams.Amd64,
		OSType:           garmParams.Linux,
		Flavor:           provider.FlavorLinuxGeneral,
		Image:            provider.RunnerImage,
		Labels:           []string{"self-hosted", "linux", "x64", "g4-synthetic", slot},
		PoolID:           g4PoolID,
		JitConfigEnabled: true,
	}
}

func g4ProviderBootstrap(in garmParams.BootstrapInstance) provider.Bootstrap {
	return provider.Bootstrap{
		Name:        in.Name,
		OSType:      string(in.OSType),
		Arch:        string(in.OSArch),
		Flavor:      in.Flavor,
		PoolID:      in.PoolID,
		CallbackURL: in.CallbackURL,
		MetadataURL: in.MetadataURL,
		Token:       in.InstanceToken,
	}
}

func g4BootstrapDoc(t *testing.T, in garmParams.BootstrapInstance) map[string]any {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func g4ComposeDoc(t *testing.T, spec provider.AppSpec) map[string]any {
	t.Helper()
	raw, err := composeConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func g4CreatePair(t *testing.T, client *g4CaptureClient) (provider.Instance, provider.Instance) {
	t.Helper()
	manager, err := provider.NewManager(client, g4ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := manager.Create(context.Background(), g4ProviderBootstrap(g4Bootstrap("alpha")))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := manager.Create(context.Background(), g4ProviderBootstrap(g4Bootstrap("beta")))
	if err != nil {
		t.Fatal(err)
	}
	if alpha.ProviderID == beta.ProviderID {
		t.Fatalf("distinct G4 requests aliased to %q", alpha.ProviderID)
	}
	if len(alpha.ProviderID) > provider.TrueNASAppNameMax || len(beta.ProviderID) > provider.TrueNASAppNameMax {
		t.Fatalf("G4 provider IDs exceed TrueNAS limit: %q / %q", alpha.ProviderID, beta.ProviderID)
	}
	return alpha, beta
}

func g4SortedIDs(instances []provider.Instance) []string {
	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		ids = append(ids, instance.ProviderID)
	}
	sort.Strings(ids)
	return ids
}

func exerciseG4RetirementOrder(t *testing.T, firstSlot, secondSlot string) {
	t.Helper()
	client := newG4CaptureClient()
	alpha, beta := g4CreatePair(t, client)
	bySlot := map[string]provider.Instance{"alpha": alpha, "beta": beta}

	fresh, err := provider.NewManager(client, g4ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := fresh.List(context.Background(), g4PoolID)
	if err != nil {
		t.Fatal(err)
	}
	got := g4SortedIDs(listed)
	want := []string{alpha.ProviderID, beta.ProviderID}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("fresh manager did not adopt exact pair: got %#v want %#v", got, want)
	}

	if err := fresh.Delete(context.Background(), bySlot[firstSlot].ProviderID); !errors.Is(err, provider.ErrActive) {
		t.Fatalf("active %s runner delete must fail closed, got %v", firstSlot, err)
	}

	first := client.apps[bySlot[firstSlot].ProviderID]
	first.State = provider.StateStopped
	client.apps[first.Spec.Name] = first
	if err := fresh.Delete(context.Background(), first.Spec.Name); err != nil {
		t.Fatal(err)
	}

	remaining, err := fresh.List(context.Background(), g4PoolID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ProviderID != bySlot[secondSlot].ProviderID {
		t.Fatalf("retiring %s corrupted peer inventory: %#v", firstSlot, remaining)
	}

	second := client.apps[bySlot[secondSlot].ProviderID]
	second.State = provider.StateStopped
	client.apps[second.Spec.Name] = second
	if err := fresh.Delete(context.Background(), second.Spec.Name); err != nil {
		t.Fatal(err)
	}
	final, err := fresh.List(context.Background(), g4PoolID)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != 0 {
		t.Fatalf("G4 retirement left provider inventory: %#v", final)
	}
}

func TestG4SourceConcurrencyReconnectAndRetirementOrders(t *testing.T) {
	exerciseG4RetirementOrder(t, "alpha", "beta")
	exerciseG4RetirementOrder(t, "beta", "alpha")
}

func TestExportNestedG4CapacityTwoFixtureBundle(t *testing.T) {
	out := os.Getenv("G4_FIXTURE_OUT")
	if out == "" {
		t.Skip("G4_FIXTURE_OUT not set")
	}

	client := newG4CaptureClient()
	alpha, beta := g4CreatePair(t, client)
	alphaBootstrap := g4Bootstrap("alpha")
	betaBootstrap := g4Bootstrap("beta")

	alphaApp := client.apps[alpha.ProviderID]
	betaApp := client.apps[beta.ProviderID]
	alphaCompose := g4ComposeDoc(t, alphaApp.Spec)
	betaCompose := g4ComposeDoc(t, betaApp.Spec)

	if alphaApp.Spec.BootstrapToken == betaApp.Spec.BootstrapToken ||
		alphaApp.Spec.CallbackURL == betaApp.Spec.CallbackURL ||
		alphaApp.Spec.MetadataURL == betaApp.Spec.MetadataURL {
		t.Fatal("G4 runner bootstrap identities are not isolated")
	}

	fresh, err := provider.NewManager(client, g4ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := fresh.List(context.Background(), g4PoolID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("fresh provider process expected two runners, got %#v", listed)
	}
	for _, instance := range []provider.Instance{alpha, beta} {
		got, err := fresh.Get(context.Background(), instance.ProviderID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ProviderID != instance.ProviderID || got.PoolID != g4PoolID {
			t.Fatalf("fresh provider Get drifted: %#v", got)
		}
	}

	// Prove both retirement permutations against fresh source-only clients.
	exerciseG4RetirementOrder(t, "alpha", "beta")
	exerciseG4RetirementOrder(t, "beta", "alpha")

	producerSource := os.Getenv("G4_PRODUCER_SOURCE")
	if producerSource == "" {
		producerSource = "unknown"
	}

	bundle := g4Bundle{
		Schema:                "semper-supra.garm-provider-truenas-g4-capacity-two-fixture/1",
		ProviderProductSource: "14535745dc3aa3c0b5466da7c704bca4d23dcec5",
		ProducerSource:        producerSource,
		ControllerID:          g4ControllerID,
		PoolID:                g4PoolID,
		Runners: []g4RunnerFixture{
			{
				Slot:              "alpha",
				BootstrapTemplate: g4BootstrapDoc(t, alphaBootstrap),
				ExpectedAppName:   alpha.ProviderID,
				ExpectedCompose:   alphaCompose,
			},
			{
				Slot:              "beta",
				BootstrapTemplate: g4BootstrapDoc(t, betaBootstrap),
				ExpectedAppName:   beta.ProviderID,
				ExpectedCompose:   betaCompose,
			},
		},
		Runner: map[string]any{
			"image":             provider.RunnerImage,
			"version":           provider.RunnerVersion,
			"tool_url":          provider.RunnerToolURL,
			"tool_filename":     provider.RunnerToolFilename,
			"tool_sha256":       provider.RunnerToolSHA256,
			"cpu":               provider.GeneralCPU,
			"memory_bytes":      provider.GeneralMemoryBytes,
			"execution_profile": provider.FlavorLinuxGeneral,
			"count":             2,
		},
		RetirementOrders: [][]string{
			{"alpha", "beta"},
			{"beta", "alpha"},
		},
		RunLocalSubstitutions: []string{
			"runners[0].bootstrap_template.callback-url",
			"runners[0].bootstrap_template.metadata-url",
			"runners[0].bootstrap_template.instance-token",
			"runners[0].expected_compose_template.services.runner.environment.GARM_CALLBACK_URL",
			"runners[0].expected_compose_template.services.runner.environment.GARM_METADATA_URL",
			"runners[0].expected_compose_template.services.runner.environment.GARM_INSTANCE_TOKEN",
			"runners[1].bootstrap_template.callback-url",
			"runners[1].bootstrap_template.metadata-url",
			"runners[1].bootstrap_template.instance-token",
			"runners[1].expected_compose_template.services.runner.environment.GARM_CALLBACK_URL",
			"runners[1].expected_compose_template.services.runner.environment.GARM_METADATA_URL",
			"runners[1].expected_compose_template.services.runner.environment.GARM_INSTANCE_TOKEN",
		},
		SourceOracles: map[string]bool{
			"two_distinct_provider_owned_names":       true,
			"true_nas_name_limit_preserved":           true,
			"fixed_profile_generated_for_each_runner": true,
			"bootstrap_identity_isolated":             true,
			"fresh_manager_adopts_exact_pair":         true,
			"active_delete_refused":                   true,
			"retirement_alpha_then_beta":              true,
			"retirement_beta_then_alpha":              true,
			"final_provider_inventory_empty":          true,
		},
		Claims: []string{
			"both bootstrap templates use the exact GARM provider-common JSON contract",
			"both App names are produced by exact provider Manager.Create and are distinct",
			"both expected Compose documents are produced by exact provider composeConfig",
			"fresh Manager reconstruction adopts exactly both same-controller/same-pool runners",
			"active deletion fails closed and both inactive retirement orders are source-qualified",
			"only callback URL, metadata URL, and synthetic instance token may be lowered per runner",
		},
		NonClaims: []string{
			"no TrueNAS runtime concurrency realization",
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
