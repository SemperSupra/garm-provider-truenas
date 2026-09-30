package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	garmParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type g3Bundle struct {
	Schema                string                       `json:"schema"`
	ProviderProductSource string                       `json:"provider_product_source"`
	ProducerSource        string                       `json:"producer_source"`
	ControllerID          string                       `json:"controller_id"`
	PoolID                string                       `json:"pool_id"`
	Bootstrap             garmParams.BootstrapInstance `json:"bootstrap"`
	Claims                []string                     `json:"claims"`
	NonClaims             []string                     `json:"non_claims"`
}

func TestExportNestedG3BootstrapFixture(t *testing.T) {
	out := os.Getenv("G3_BOOTSTRAP_OUT")
	if out == "" {
		t.Skip("G3_BOOTSTRAP_OUT not set")
	}

	osName := "linux"
	arch := "x64"
	url := provider.RunnerToolURL
	filename := provider.RunnerToolFilename
	sha := provider.RunnerToolSHA256
	emptyToken := ""

	bootstrap := garmParams.BootstrapInstance{
		Name: "g3-runner-1",
		Tools: []garmParams.RunnerApplicationDownload{{
			OS:                &osName,
			Architecture:      &arch,
			DownloadURL:       &url,
			Filename:          &filename,
			TempDownloadToken: &emptyToken,
			SHA256Checksum:    &sha,
		}},
		RepoURL:          "https://github.com/actions/runner",
		CallbackURL:      "https://127.0.0.1:1/callbacks",
		MetadataURL:      "https://127.0.0.1:1/metadata",
		InstanceToken:    "g3-public-synthetic-bootstrap-token",
		SSHKeys:          []string{},
		ExtraSpecs:       json.RawMessage(`{"runner_install_template":"g3-public-garm-internal-compat-fixture"}`),
		GitHubRunnerGroup: "",
		CACertBundle:     []byte{},
		OSArch:           garmParams.Amd64,
		OSType:           garmParams.Linux,
		Flavor:           provider.FlavorLinuxGeneral,
		Image:            provider.RunnerImage,
		Labels:           []string{"g3-public-fixture"},
		PoolID:           "g3-pool",
		JitConfigEnabled: true,
	}

	if err := validateBootstrapContract(bootstrap); err != nil {
		t.Fatalf("source-exact G3 bootstrap fixture rejected: %v", err)
	}

	producerSource := os.Getenv("G3_PRODUCER_SOURCE")
	if producerSource == "" {
		producerSource = "unknown"
	}

	bundle := g3Bundle{
		Schema:                "semper-supra.garm-provider-truenas-g3-bootstrap/1",
		ProviderProductSource: "14535745dc3aa3c0b5466da7c704bca4d23dcec5",
		ProducerSource:        producerSource,
		ControllerID:          "g3-controller",
		PoolID:                "g3-pool",
		Bootstrap:             bootstrap,
		Claims: []string{
			"bootstrap is accepted by the exact provider CreateInstance validation contract",
			"callback and metadata are HTTPS but intentionally container-local and unreachable",
			"runner tool metadata is the exact provider-pinned public GitHub runner release",
			"only GARM runner_install_template compatibility metadata is admitted in create extra specs",
		},
		NonClaims: []string{
			"no GitHub registration credential",
			"no JIT metadata service",
			"no runner registration success",
			"no TrueNAS runtime realization",
			"no physical-host qualification",
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
