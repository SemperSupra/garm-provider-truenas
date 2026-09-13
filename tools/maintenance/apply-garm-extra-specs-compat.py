#!/usr/bin/env python3
from pathlib import Path

provider = Path("cmd/garm-provider-truenas/garm_provider.go")
text = provider.read_text(encoding="utf-8")
old_call = "\tif err := validateEmptyExtraSpecs(bootstrap.ExtraSpecs); err != nil {\n\t\treturn err\n\t}\n"
new_call = "\tif err := validateCreateExtraSpecs(bootstrap.ExtraSpecs); err != nil {\n\t\treturn err\n\t}\n"
if text.count(old_call) != 1:
    raise SystemExit(f"expected exactly one bootstrap extra-spec validator call, found {text.count(old_call)}")
text = text.replace(old_call, new_call, 1)
anchor = "func loadConfig(path string) (config, error) {\n"
if text.count(anchor) != 1:
    raise SystemExit("loadConfig anchor drifted")
compat = r'''func validateCreateExtraSpecs(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("extra specs must be a JSON object: %w", err)
	}
	if len(obj) == 0 {
		return nil
	}
	if len(obj) != 1 {
		return errors.New("create extra specs may only contain GARM runner_install_template compatibility metadata")
	}
	rawTemplate, ok := obj["runner_install_template"]
	if !ok {
		return errors.New("create extra specs may only contain GARM runner_install_template compatibility metadata")
	}
	var template string
	if err := json.Unmarshal(rawTemplate, &template); err != nil {
		return errors.New("GARM runner_install_template compatibility metadata must be a string")
	}
	if strings.TrimSpace(template) == "" {
		return errors.New("GARM runner_install_template compatibility metadata must not be empty")
	}
	return nil
}

'''
text = text.replace(anchor, compat + anchor, 1)
provider.write_text(text, encoding="utf-8")

test = Path("cmd/garm-provider-truenas/extra_specs_compat_test.go")
if test.exists():
    raise SystemExit(f"unexpected existing test file: {test}")
test.write_text(r'''package main

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
		"non-string": `{"runner_install_template":{"unexpected":true}}`,
		"empty":      `{"runner_install_template":"   "}`,
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
''', encoding="utf-8")
