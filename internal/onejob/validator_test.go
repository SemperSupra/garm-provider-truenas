package onejob

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func validFixture() (WorkloadRequest, Receipt) {
	nonce := "onejob-beta3-20261119-abcdef01"
	payload := "nonce=" + nonce + "\nhostname=garm-runner-1\nstatus=ok\n"
	sum := sha256.Sum256([]byte(payload))

	req := WorkloadRequest{
		RunNonce:           nonce,
		Repository:         "SemperSupra/garm-runner-smoke",
		ExpectedRunnerName: "garm-runner-1",
		ExpectedBackend:    "container",
		ExpectedTarget:     "26.0.0-BETA.3",
		TriggerEvent:       AllowedTriggerEvent,
		Permissions:        map[string]string{"contents": "read"},
		ExpectedAssertions: []string{"uname_linux_x86_64", "nonce_echo", "no_bootstrap_token_env"},
		PrivateRepoAllowed: false,
	}

	receipt := Receipt{
		Schema:               ReceiptSchemaV1,
		RunNonce:             nonce,
		Repository:           req.Repository,
		TriggerEvent:         AllowedTriggerEvent,
		Permissions:          map[string]string{"contents": "read"},
		GitHubRunID:          44599900001,
		GitHubJobConclusion:  "success",
		RunnerName:           req.ExpectedRunnerName,
		InGuestHostname:      "garm-runner-1",
		Backend:              "container",
		TargetVersion:        "26.0.0-BETA.3",
		ArtifactName:         "one-job-receipt-" + nonce,
		ArtifactSHA256:       hex.EncodeToString(sum[:]),
		ArtifactPayload:      payload,
		PassedAssertions:     []string{"uname_linux_x86_64", "nonce_echo", "no_bootstrap_token_env"},
		RunnerDeregistered:   true,
		HostCleanupAttempted: true,
		HostCleanupAbsent:    true,
		ZeroResidueVerified:  true,
	}
	return req, receipt
}

func TestValidateReceiptAcceptsValidOneJobProof(t *testing.T) {
	req, receipt := validFixture()
	if err := ValidateReceipt(req, receipt); err != nil {
		t.Fatalf("expected valid receipt to pass, got: %v", err)
	}
}

func TestValidateReceiptFailsClosedOnViolations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*WorkloadRequest, *Receipt)
		wantErr string
	}{
		{
			name:    "untrusted PR trigger rejected",
			mutate:  func(req *WorkloadRequest, _ *Receipt) { req.TriggerEvent = "pull_request" },
			wantErr: "forbidden",
		},
		{
			name: "excessive write permission rejected",
			mutate: func(req *WorkloadRequest, _ *Receipt) {
				req.Permissions = map[string]string{"contents": "write"}
			},
			wantErr: "permissions",
		},
		{
			name:    "private repo flag rejected",
			mutate:  func(req *WorkloadRequest, _ *Receipt) { req.PrivateRepoAllowed = true },
			wantErr: "private_repo_allowed",
		},
		{
			name:    "nonce mismatch rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.RunNonce = "onejob-beta3-20261119-different" },
			wantErr: "run_nonce mismatch",
		},
		{
			name:    "artifact digest tamper rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.ArtifactPayload += "tampered\n" },
			wantErr: "does not match",
		},
		{
			name:    "missing assertion rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.PassedAssertions = []string{"uname_linux_x86_64"} },
			wantErr: "missing from passed_assertions",
		},
		{
			name:    "runner not deregistered rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.RunnerDeregistered = false },
			wantErr: "runner_deregistered",
		},
		{
			name:    "host residue rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.HostCleanupAbsent = false },
			wantErr: "zero-residue",
		},
		{
			name:    "missing guest hostname rejected",
			mutate:  func(_ *WorkloadRequest, r *Receipt) { r.InGuestHostname = "" },
			wantErr: "in_guest_hostname",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, receipt := validFixture()
			tc.mutate(&req, &receipt)
			err := ValidateReceipt(req, receipt)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
