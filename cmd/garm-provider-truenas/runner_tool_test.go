package main

import (
	"strings"
	"testing"
)

func TestRunnerToolContractAcceptsTestedLinuxX64Release(t *testing.T) {
	if err := validateRunnerToolContract(validBootstrap()); err != nil {
		t.Fatalf("tested runner tool contract rejected: %v", err)
	}
}

func TestRunnerToolContractRejectsMissingTool(t *testing.T) {
	bootstrap := validBootstrap()
	bootstrap.Tools = nil
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "Linux/x64") {
		t.Fatalf("missing runner tool should fail closed, got %v", err)
	}
}

func TestRunnerToolContractRejectsChecksumDrift(t *testing.T) {
	bootstrap := validBootstrap()
	bad := strings.Repeat("0", 64)
	bootstrap.Tools[0].SHA256Checksum = &bad
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("runner checksum drift should fail closed, got %v", err)
	}
}

func TestRunnerToolContractRejectsURLDrift(t *testing.T) {
	bootstrap := validBootstrap()
	bad := "https://example.invalid/actions-runner.tar.gz"
	bootstrap.Tools[0].DownloadURL = &bad
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("runner URL drift should fail closed, got %v", err)
	}
}

func TestRunnerToolContractRejectsTemporaryDownloadToken(t *testing.T) {
	bootstrap := validBootstrap()
	token := "short-lived-test-token"
	bootstrap.Tools[0].TempDownloadToken = &token
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "temporary runner download tokens") {
		t.Fatalf("temporary download token should fail closed, got %v", err)
	}
}
