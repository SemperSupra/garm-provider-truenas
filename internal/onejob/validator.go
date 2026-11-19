package onejob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// ReceiptSchemaV1 identifies the independent one-job runner validation receipt.
	ReceiptSchemaV1 = "semper-supra.garm-one-job-receipt/1"

	// AllowedTriggerEvent restricts validation runs to manual/brokered dispatch;
	// untrusted PR triggers are rejected fail-closed.
	AllowedTriggerEvent = "workflow_dispatch"

	// RequiredPermissionsContents enforces minimal read-only repository scope.
	RequiredPermissionsContents = "read"
)

var (
	noncePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{15,127}$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repoPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// WorkloadRequest defines the expected public-safe one-job validation contract
// before a single ephemeral runner executes on a dedicated controlled test repo.
type WorkloadRequest struct {
	RunNonce            string            `json:"run_nonce"`
	Repository          string            `json:"repository"`
	ExpectedRunnerName  string            `json:"expected_runner_name"`
	ExpectedBackend     string            `json:"expected_backend"`
	ExpectedTarget      string            `json:"expected_target"`
	TriggerEvent        string            `json:"trigger_event"`
	Permissions         map[string]string `json:"permissions"`
	ExpectedAssertions  []string          `json:"expected_assertions"`
	PrivateRepoAllowed  bool              `json:"private_repo_allowed"`
}

// Receipt captures independent host-side and GitHub-side evidence for one
// ephemeral runner job without relying solely on a green workflow wrapper.
type Receipt struct {
	Schema               string            `json:"schema"`
	RunNonce             string            `json:"run_nonce"`
	Repository           string            `json:"repository"`
	TriggerEvent         string            `json:"trigger_event"`
	Permissions          map[string]string `json:"permissions"`
	GitHubRunID          int64             `json:"github_run_id"`
	GitHubJobConclusion  string            `json:"github_job_conclusion"`
	RunnerName           string            `json:"runner_name"`
	InGuestHostname      string            `json:"in_guest_hostname"`
	Backend              string            `json:"backend"`
	TargetVersion        string            `json:"target_version"`
	ArtifactName         string            `json:"artifact_name"`
	ArtifactSHA256       string            `json:"artifact_sha256"`
	ArtifactPayload      string            `json:"artifact_payload,omitempty"`
	PassedAssertions     []string          `json:"passed_assertions"`
	RunnerDeregistered   bool              `json:"runner_deregistered"`
	HostCleanupAttempted bool              `json:"host_cleanup_attempted"`
	HostCleanupAbsent    bool              `json:"host_cleanup_absent"`
	ZeroResidueVerified  bool              `json:"zero_residue_verified"`
}

// ValidateRequest fails closed if a one-job request violates public-safe
// trigger, permission, nonce, or repository boundaries.
func ValidateRequest(req WorkloadRequest) error {
	if !noncePattern.MatchString(req.RunNonce) {
		return fmt.Errorf("invalid run_nonce %q: must be 16-128 safe identifier characters", req.RunNonce)
	}
	if !repoPattern.MatchString(req.Repository) {
		return fmt.Errorf("invalid repository %q: must be owner/repo", req.Repository)
	}
	if req.PrivateRepoAllowed {
		return errors.New("private_repo_allowed must be false for public-safe one-job validation")
	}
	if strings.TrimSpace(req.ExpectedRunnerName) == "" {
		return errors.New("expected_runner_name is required")
	}
	if strings.TrimSpace(req.ExpectedBackend) == "" || strings.TrimSpace(req.ExpectedTarget) == "" {
		return errors.New("expected_backend and expected_target are required")
	}
	if req.TriggerEvent != AllowedTriggerEvent {
		return fmt.Errorf("trigger_event %q is forbidden: only %q is allowed", req.TriggerEvent, AllowedTriggerEvent)
	}
	if err := validateMinimalPermissions(req.Permissions); err != nil {
		return err
	}
	if len(req.ExpectedAssertions) == 0 {
		return errors.New("at least one expected assertion is required")
	}
	return nil
}

// ValidateReceipt independently verifies that one ephemeral runner executed the
// exact requested nonce/assertions, produced a verified artifact digest,
// deregistered from GitHub, and left zero host residue.
func ValidateReceipt(req WorkloadRequest, receipt Receipt) error {
	if err := ValidateRequest(req); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if receipt.Schema != ReceiptSchemaV1 {
		return fmt.Errorf("unexpected receipt schema %q, want %q", receipt.Schema, ReceiptSchemaV1)
	}
	if receipt.RunNonce != req.RunNonce {
		return fmt.Errorf("run_nonce mismatch: got %q, want %q", receipt.RunNonce, req.RunNonce)
	}
	if receipt.Repository != req.Repository {
		return fmt.Errorf("repository mismatch: got %q, want %q", receipt.Repository, req.Repository)
	}
	if receipt.TriggerEvent != AllowedTriggerEvent {
		return fmt.Errorf("receipt trigger_event %q is forbidden", receipt.TriggerEvent)
	}
	if err := validateMinimalPermissions(receipt.Permissions); err != nil {
		return fmt.Errorf("receipt permissions invalid: %w", err)
	}
	if receipt.GitHubRunID <= 0 {
		return errors.New("github_run_id must be positive")
	}
	if receipt.GitHubJobConclusion != "success" {
		return fmt.Errorf("github_job_conclusion = %q, want \"success\"", receipt.GitHubJobConclusion)
	}
	if receipt.RunnerName != req.ExpectedRunnerName {
		return fmt.Errorf("runner_name mismatch: got %q, want %q", receipt.RunnerName, req.ExpectedRunnerName)
	}
	if strings.TrimSpace(receipt.InGuestHostname) == "" {
		return errors.New("in_guest_hostname is required for host-vs-guest identity proof")
	}
	if receipt.Backend != req.ExpectedBackend {
		return fmt.Errorf("backend mismatch: got %q, want %q", receipt.Backend, req.ExpectedBackend)
	}
	if receipt.TargetVersion != req.ExpectedTarget {
		return fmt.Errorf("target_version mismatch: got %q, want %q", receipt.TargetVersion, req.ExpectedTarget)
	}
	if strings.TrimSpace(receipt.ArtifactName) == "" {
		return errors.New("artifact_name is required")
	}
	if !sha256Pattern.MatchString(receipt.ArtifactSHA256) {
		return fmt.Errorf("artifact_sha256 %q is not a valid lowercase hex sha256", receipt.ArtifactSHA256)
	}
	if receipt.ArtifactPayload != "" {
		sum := sha256.Sum256([]byte(receipt.ArtifactPayload))
		actual := hex.EncodeToString(sum[:])
		if actual != receipt.ArtifactSHA256 {
			return fmt.Errorf("artifact_payload sha256 %q does not match receipt artifact_sha256 %q", actual, receipt.ArtifactSHA256)
		}
		if !strings.Contains(receipt.ArtifactPayload, req.RunNonce) {
			return errors.New("artifact_payload does not contain the requested run_nonce")
		}
	}

	passed := make(map[string]struct{}, len(receipt.PassedAssertions))
	for _, a := range receipt.PassedAssertions {
		passed[a] = struct{}{}
	}
	for _, want := range req.ExpectedAssertions {
		if _, ok := passed[want]; !ok {
			return fmt.Errorf("required assertion %q missing from passed_assertions", want)
		}
	}

	if !receipt.RunnerDeregistered {
		return errors.New("runner_deregistered must be true")
	}
	if !receipt.HostCleanupAttempted || !receipt.HostCleanupAbsent || !receipt.ZeroResidueVerified {
		return errors.New("host cleanup and zero-residue verification must all be true")
	}
	return nil
}

func validateMinimalPermissions(perms map[string]string) error {
	if len(perms) != 1 || perms["contents"] != RequiredPermissionsContents {
		return fmt.Errorf("permissions must be strictly {\"contents\":\"read\"}, got %v", perms)
	}
	return nil
}
