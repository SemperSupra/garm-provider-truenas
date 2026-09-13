package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateAcceptsGARMRunnerInstallTemplateCompatibilityMetadata(t *testing.T) {
	bootstrap := validBootstrap()
	bootstrap.ExtraSpecs = json.RawMessage(`{"runner_install_template":"#!/usr/bin/env bash\\necho generated-by-garm"}`)
	if err := validateBootstrapContract(bootstrap); err != nil {
		t.Fatalf("GARM-generated runner_install_template should be accepted at create time: %v", err)
	}
}

func TestCreateRejectsUnknownExtraSpecAlongsideGARMCompatibilityMetadata(t *testing.T) {
	bootstrap := validBootstrap()
	bootstrap.ExtraSpecs = json.RawMessage(`{"runner_install_template":"generated","surprise":true}`)
	if err := validateBootstrapContract(bootstrap); err == nil {
		t.Fatal("unknown create extra spec unexpectedly accepted")
	} else if !strings.Contains(err.Error(), "runner_install_template") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRejectsMalformedGARMCompatibilityMetadata(t *testing.T) {
	for name, raw := range map[string]string{
		"non-string":   `{"runner_install_template":{"unexpected":true}}`,
		"empty":        `{"runner_install_template":"   "}`,
		"unknown-only": `{"surprise":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			bootstrap := validBootstrap()
			bootstrap.ExtraSpecs = json.RawMessage(raw)
			if err := validateBootstrapContract(bootstrap); err == nil {
				t.Fatal("malformed compatibility metadata unexpectedly accepted")
			}
		})
	}
}

func TestPoolExtraSpecsRemainStrictlyEmpty(t *testing.T) {
	if err := validateEmptyExtraSpecs([]byte(`{"runner_install_template":"generated"}`)); err == nil {
		t.Fatal("pool configuration unexpectedly accepted the create-time compatibility field")
	}
}
