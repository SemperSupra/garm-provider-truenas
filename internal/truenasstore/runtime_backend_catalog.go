package truenasstore

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const RuntimeBackendMatrixSchema = "semper-supra.garm-provider-truenas-runtime-backend-target-matrix/1"

var ErrRuntimeTargetUnknown = errors.New("TrueNAS runtime target is not in the exact backend matrix")
var ErrRuntimeBackendUnknown = errors.New("TrueNAS runtime backend is not in the backend matrix")

// runtimeBackendMatrixJSON remains physically under testdata because it began as
// qualification evidence. Embedding that exact file makes it the single source
// of truth for both runtime driver resolution and validation; no second registry
// is introduced.
//
//go:embed testdata/runtime-backend-target-matrix.json
var runtimeBackendMatrixJSON []byte

type RuntimeBackendCell struct {
	Status           string            `json:"status"`
	Driver           string            `json:"driver"`
	ControlSurface   string            `json:"control_surface"`
	MiddlewareCommit string            `json:"middleware_commit"`
	SourceBlobs      map[string]string `json:"source_blobs"`
	RequiredMethods  []string          `json:"required_methods"`
	Note             string            `json:"note,omitempty"`
}

type RuntimeBackendEntry struct {
	ConfigID             string                        `json:"config_id"`
	ProviderName         string                        `json:"provider_name"`
	Profile              string                        `json:"profile"`
	ImplementationStatus string                        `json:"implementation_status"`
	TargetStatus         map[string]RuntimeBackendCell `json:"target_status"`
}

type RuntimeBackendMatrix struct {
	Schema                           string                         `json:"schema"`
	Authority                        string                         `json:"authority"`
	ArchitectureAuthority            string                         `json:"architecture_authority"`
	RuntimeInheritanceAllowed        bool                           `json:"runtime_inheritance_allowed"`
	RequiredTargetVersions           []string                       `json:"required_target_versions"`
	Backends                         map[string]RuntimeBackendEntry `json:"backends"`
	AllAdmittedRuntimeCellsQualified bool                           `json:"all_admitted_runtime_cells_qualified"`
	ClaimBoundary                    string                         `json:"claim_boundary"`
}

func LoadRuntimeBackendMatrix() (RuntimeBackendMatrix, error) {
	var matrix RuntimeBackendMatrix
	if err := json.Unmarshal(runtimeBackendMatrixJSON, &matrix); err != nil {
		return matrix, fmt.Errorf("decode embedded runtime backend matrix: %w", err)
	}
	if matrix.Schema != RuntimeBackendMatrixSchema {
		return matrix, fmt.Errorf("unsupported runtime backend matrix schema %q", matrix.Schema)
	}
	if matrix.RuntimeInheritanceAllowed {
		return matrix, errors.New("runtime backend matrix illegally enables support inheritance")
	}
	return matrix, nil
}

// ResolveRuntimeBackend returns the exact-version driver contract. It does not
// itself promote OPEN cells to operational support; qualification code needs
// to resolve OPEN rows in order to test them.
func ResolveRuntimeBackend(backend, systemVersion string) (RuntimeBackendEntry, RuntimeBackendCell, error) {
	matrix, err := LoadRuntimeBackendMatrix()
	if err != nil {
		return RuntimeBackendEntry{}, RuntimeBackendCell{}, err
	}
	backend = strings.ToLower(strings.TrimSpace(backend))
	entry, ok := matrix.Backends[backend]
	if !ok {
		return RuntimeBackendEntry{}, RuntimeBackendCell{}, fmt.Errorf("%w: %q", ErrRuntimeBackendUnknown, backend)
	}
	version := strings.TrimPrefix(strings.TrimSpace(systemVersion), "TrueNAS-")
	cell, ok := entry.TargetStatus[version]
	if !ok {
		return RuntimeBackendEntry{}, RuntimeBackendCell{}, fmt.Errorf("%w: %q", ErrRuntimeTargetUnknown, version)
	}
	return entry, cell, nil
}

func RuntimeCellOperationallyAdmitted(cell RuntimeBackendCell) bool {
	return cell.Status == "PASS"
}
