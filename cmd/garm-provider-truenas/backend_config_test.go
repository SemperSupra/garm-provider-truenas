package main

import (
	"strings"
	"testing"
)

func baseTrueNASConfig() config {
	return config{
		Mode: "truenas",
		TrueNAS: &trueNASConfig{
			Host:     "truenas.example.invalid",
			Username: "garm",
		},
	}
}

func TestTrueNASBackendDefaultsToApps(t *testing.T) {
	cfg := baseTrueNASConfig()
	if got := normalizeTrueNASBackend(cfg.TrueNAS.Backend); got != trueNASBackendApps {
		t.Fatalf("default backend = %q, want %q", got, trueNASBackendApps)
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("legacy TrueNAS config must remain valid with implicit Apps backend: %v", err)
	}

	cfg.TrueNAS.Backend = "apps"
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("explicit Apps backend must be valid: %v", err)
	}
}

func TestTrueNASFutureBackendsFailClosedUntilImplemented(t *testing.T) {
	for _, backend := range []string{"container", "vm"} {
		t.Run(backend, func(t *testing.T) {
			cfg := baseTrueNASConfig()
			cfg.TrueNAS.Backend = backend
			err := validateConfig(cfg)
			if err == nil {
				t.Fatalf("backend %q unexpectedly admitted before implementation", backend)
			}
			if !strings.Contains(err.Error(), "recognized but not yet implemented/qualified") {
				t.Fatalf("backend %q returned unexpected error: %v", backend, err)
			}
		})
	}
}

func TestTrueNASUnknownBackendFailsClosed(t *testing.T) {
	cfg := baseTrueNASConfig()
	cfg.TrueNAS.Backend = "magic"
	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("unknown backend unexpectedly admitted")
	}
	if !strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderConfigSchemaExposesStableBackendIDs(t *testing.T) {
	for _, want := range []string{
		`"backend": {"type": "string", "enum": ["apps", "container", "vm"], "default": "apps"}`,
	} {
		if !strings.Contains(providerConfigSchema, want) {
			t.Fatalf("provider schema missing backend contract %q", want)
		}
	}
}
