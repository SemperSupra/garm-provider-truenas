package main

import (
	"strings"
	"testing"
)

func TestRunnerToolContractAcceptsPinnedLinuxX64Release(t *testing.T) {
	if err := validateRunnerToolContract(validBootstrap()); err != nil {
		t.Fatalf("pinned runner tool contract rejected: %v", err)
	}
}

func TestRunnerToolContractAcceptsNewerOfficialGitHubReleaseMetadata(t *testing.T) {
	bootstrap := validBootstrap()
	filename := "actions-runner-linux-x64-2.337.0.tar.gz"
	downloadURL := "https://github.com/actions/runner/releases/download/v2.337.0/" + filename
	checksum := "70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613"
	bootstrap.Tools[0].Filename = &filename
	bootstrap.Tools[0].DownloadURL = &downloadURL
	bootstrap.Tools[0].SHA256Checksum = &checksum
	if err := validateRunnerToolContract(bootstrap); err != nil {
		t.Fatalf("official newer GitHub runner metadata should not force provider runtime-pin drift: %v", err)
	}
}

func TestRunnerToolContractRejectsMissingTool(t *testing.T) {
	bootstrap := validBootstrap()
	bootstrap.Tools = nil
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "Linux/x64") {
		t.Fatalf("missing runner tool should fail closed, got %v", err)
	}
}

func TestRunnerToolContractRejectsMalformedChecksum(t *testing.T) {
	bootstrap := validBootstrap()
	bad := strings.Repeat("0", 64)
	bootstrap.Tools[0].SHA256Checksum = &bad
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("invalid runner checksum should fail closed, got %v", err)
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

func TestRunnerToolContractRejectsFilenameURLVersionMismatch(t *testing.T) {
	bootstrap := validBootstrap()
	filename := "actions-runner-linux-x64-2.337.0.tar.gz"
	bootstrap.Tools[0].Filename = &filename
	if err := validateRunnerToolContract(bootstrap); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("runner filename/URL version mismatch should fail closed, got %v", err)
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
