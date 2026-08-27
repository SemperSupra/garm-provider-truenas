package truenasstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	truenas "github.com/deevus/truenas-go"
	tnclient "github.com/deevus/truenas-go/client"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

func fixedSpec() provider.AppSpec {
	return provider.AppSpec{
		Name:              "garm-controller-runner-1",
		Image:             provider.RunnerImage,
		ControllerID:      "controller-123",
		PoolID:            "pool-123",
		CPU:               provider.GeneralCPU,
		MemoryBytes:       provider.GeneralMemoryBytes,
		RunAsUser:         "1001:1001",
		CapDrop:           []string{"ALL"},
		NoNewPrivileges:   true,
		WorkdirTmpfs:      false,
		CredentialTmpfs:   true,
		HostMounts:        []string{},
		DockerSocket:      false,
		CallbackURL:       "https://garm.example.invalid/api/v1/callbacks",
		MetadataURL:       "https://garm.example.invalid/api/v1/metadata",
		BootstrapToken:    "ephemeral-test-token",
		RunnerDownloadURL: provider.RunnerToolURL,
		RunnerFilename:    provider.RunnerToolFilename,
		RunnerSHA256:      provider.RunnerToolSHA256,
		ExecutionProfile:  provider.FlavorLinuxGeneral,
	}
}

func appFromCompose(name, state, compose string) *truenas.App {
	return &truenas.App{
		Name:  name,
		State: state,
		Config: map[string]any{
			"custom_compose_config_string": compose,
		},
	}
}

func TestCreateUsesFixedCustomComposeAndRecoversOwnership(t *testing.T) {
	spec := fixedSpec()
	var captured truenas.CreateAppOpts
	var compose string
	apps := &truenas.MockAppService{
		CreateAppFunc: func(_ context.Context, opts truenas.CreateAppOpts) (*truenas.App, error) {
			captured = opts
			compose = opts.CustomComposeConfig
			return &truenas.App{Name: opts.Name}, nil
		},
		GetAppWithConfigFunc: func(_ context.Context, name string) (*truenas.App, error) {
			return appFromCompose(name, "RUNNING", compose), nil
		},
	}
	store := New(apps, &tnclient.MockClient{})

	got, err := store.CreateApp(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !captured.CustomApp || captured.Name != spec.Name || captured.CustomComposeConfig == "" {
		t.Fatalf("unexpected create options: %#v", captured)
	}
	if got.Spec.ControllerID != spec.ControllerID || got.Spec.PoolID != spec.PoolID {
		t.Fatalf("ownership did not round-trip: %#v", got.Spec)
	}
	if got.State != provider.StateRunning {
		t.Fatalf("unexpected state: %s", got.State)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(captured.CustomComposeConfig), &doc); err != nil {
		t.Fatalf("Compose is not deterministic JSON/YAML: %v", err)
	}
	services := doc["services"].(map[string]any)
	runner := services["runner"].(map[string]any)
	if runner["image"] != provider.RunnerImage {
		t.Fatalf("unexpected image: %v", runner["image"])
	}
	labels := runner["labels"].(map[string]any)
	if labels[labelManaged] != "true" || labels[labelController] != spec.ControllerID || labels[labelPool] != spec.PoolID {
		t.Fatalf("ownership labels missing: %#v", labels)
	}
	if _, ok := runner["volumes"]; ok {
		t.Fatal("host volume configuration must not be generated")
	}
	if runner["privileged"] == true {
		t.Fatal("privileged runner must not be generated")
	}

	entrypoint, ok := runner["entrypoint"].([]any)
	if !ok || len(entrypoint) != 3 || entrypoint[0] != "/bin/sh" || entrypoint[1] != "-c" {
		t.Fatalf("bootstrap must use deterministic shell entrypoint: %#v", runner["entrypoint"])
	}
	if script, _ := entrypoint[2].(string); !strings.Contains(script, "sha256sum -c") || !strings.Contains(script, "credentials/runner") {
		t.Fatalf("bootstrap entrypoint is missing verified download/JIT behavior")
	}

	tmpfs, ok := runner["tmpfs"].([]any)
	if !ok || len(tmpfs) != 1 {
		t.Fatalf("expected credential-only tmpfs, got %#v", runner["tmpfs"])
	}
	mount, _ := tmpfs[0].(string)
	if !strings.HasPrefix(mount, "/run/garm-jit:") || !strings.Contains(mount, "noexec") {
		t.Fatalf("JIT tmpfs must be non-executable: %q", mount)
	}
	if strings.Contains(mount, "/home/runner/_work") || strings.Contains(mount, "/home/runner/actions-runner") {
		t.Fatalf("runner/work executable paths must not be tmpfs mounted: %q", mount)
	}

	env := runner["environment"].(map[string]any)
	if env["GARM_RUNNER_DOWNLOAD_URL"] != provider.RunnerToolURL || env["GARM_RUNNER_FILENAME"] != provider.RunnerToolFilename || env["GARM_RUNNER_SHA256"] != provider.RunnerToolSHA256 {
		t.Fatalf("verified runner payload metadata missing from Compose: %#v", env)
	}
	if _, ok := env["GARM_RUNNER_TEMP_DOWNLOAD_TOKEN"]; ok {
		t.Fatal("temporary runner download token must not be persisted in Compose")
	}
}

func TestComposeRejectsProfileEscape(t *testing.T) {
	spec := fixedSpec()
	spec.HostMounts = []string{"/mnt/tank:/data"}
	if _, err := composeConfig(spec); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("host mount escape should be rejected, got %v", err)
	}

	spec = fixedSpec()
	spec.Image = "ubuntu:latest"
	if _, err := composeConfig(spec); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("mutable/unapproved image should be rejected, got %v", err)
	}

	spec = fixedSpec()
	spec.WorkdirTmpfs = true
	if _, err := composeConfig(spec); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("noexec workdir tmpfs should be rejected, got %v", err)
	}

	spec = fixedSpec()
	spec.RunnerSHA256 = strings.Repeat("0", 64)
	if _, err := composeConfig(spec); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("unverified runner payload should be rejected, got %v", err)
	}
}

func TestUnknownTrueNASStateFailsClosedAsActive(t *testing.T) {
	if got := mapState("MYSTERY"); got != provider.StateDeploying {
		t.Fatalf("unknown state must map to fail-closed active state, got %s", got)
	}
}

func TestGetRejectsUnmanagedNameCollision(t *testing.T) {
	apps := &truenas.MockAppService{
		GetAppWithConfigFunc: func(_ context.Context, name string) (*truenas.App, error) {
			return &truenas.App{
				Name:  name,
				State: "STOPPED",
				Config: map[string]any{
					"services": map[string]any{
						"runner": map[string]any{
							"image":  provider.RunnerImage,
							"labels": map[string]any{},
						},
					},
				},
			}, nil
		},
	}
	store := New(apps, &tnclient.MockClient{})
	if _, err := store.GetApp(context.Background(), "garm-controller-runner-1"); !errors.Is(err, provider.ErrForeign) {
		t.Fatalf("unmanaged collision should be foreign, got %v", err)
	}
}

func TestListSkipsNonGARMAppsAndKeepsForeignManagedAppsForManagerFiltering(t *testing.T) {
	local := fixedSpec()
	foreign := fixedSpec()
	foreign.Name = "garm-other-runner-2"
	foreign.ControllerID = "other-controller"

	localCompose, _ := composeConfig(local)
	foreignCompose, _ := composeConfig(foreign)
	configs := map[string]string{local.Name: localCompose, foreign.Name: foreignCompose}
	apps := &truenas.MockAppService{
		ListAppsFunc: func(context.Context) ([]truenas.App, error) {
			return []truenas.App{
				{Name: "plex", State: "RUNNING"},
				{Name: foreign.Name, State: "STOPPED"},
				{Name: local.Name, State: "RUNNING"},
			}, nil
		},
		GetAppWithConfigFunc: func(_ context.Context, name string) (*truenas.App, error) {
			return appFromCompose(name, "STOPPED", configs[name]), nil
		},
	}
	store := New(apps, &tnclient.MockClient{})
	got, err := store.ListApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two managed apps, got %#v", got)
	}
	controllers := []string{got[0].Spec.ControllerID, got[1].Spec.ControllerID}
	if !reflect.DeepEqual(controllers, []string{"controller-123", "other-controller"}) && !reflect.DeepEqual(controllers, []string{"other-controller", "controller-123"}) {
		t.Fatalf("unexpected controllers: %#v", controllers)
	}
}

func TestDeleteUsesExplicitNonVolumeDestructiveOptions(t *testing.T) {
	var method string
	var params any
	caller := &tnclient.MockClient{
		CallAndWaitFunc: func(_ context.Context, gotMethod string, gotParams any) (json.RawMessage, error) {
			method, params = gotMethod, gotParams
			return nil, nil
		},
	}
	store := New(&truenas.MockAppService{}, caller)
	if err := store.DeleteApp(context.Background(), "garm-controller-runner-1"); err != nil {
		t.Fatal(err)
	}
	if method != "app.delete" {
		t.Fatalf("unexpected delete method: %s", method)
	}
	list, ok := params.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("unexpected delete params: %#v", params)
	}
	opts, ok := list[1].(map[string]any)
	if !ok || opts["remove_images"] != false || opts["remove_ix_volumes"] != false {
		t.Fatalf("delete options are not fail-closed: %#v", list[1])
	}
}

func TestConnectRejectsInsecureTLSBeforeNetwork(t *testing.T) {
	_, _, err := Connect(context.Background(), Config{
		Host:               "truenas.example.invalid",
		Username:           "automation",
		APIKey:             "test-only-key",
		InsecureSkipVerify: true,
	})
	if err == nil {
		t.Fatal("insecure TLS configuration unexpectedly accepted")
	}
}
