package main

import (
	"os"
	"strings"
	"testing"
)

func TestCallbackHostGatewayConfigAndSchema(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "provider-*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if _, err := f.WriteString(`{"mode":"truenas","truenas":{"host":"truenas.example.invalid","username":"svc","callback_host_gateway":true}}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrueNAS == nil || !cfg.TrueNAS.CallbackHostGateway {
		t.Fatalf("callback_host_gateway did not round-trip: %#v", cfg.TrueNAS)
	}
	if !strings.Contains(providerConfigSchema, `"callback_host_gateway": {"type": "boolean", "default": false}`) {
		t.Fatal("provider JSON schema does not expose callback_host_gateway as a bounded boolean")
	}
}

func TestCallbackHostGatewayRejectsNonBooleanConfig(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "provider-*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if _, err := f.WriteString(`{"mode":"truenas","truenas":{"host":"truenas.example.invalid","username":"svc","callback_host_gateway":"garm.example.invalid"}}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("non-boolean callback_host_gateway must be rejected")
	}
}
