package truenasstore

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	tnclient "github.com/deevus/truenas-go/client"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

const ixAppsContractFixture = "testdata/truenas-25.04.1-apps-contract.json"

type ixAppsContract struct {
	SchemaVersion int    `json:"schema_version"`
	ProfileID     string `json:"profile_id"`
	Middleware    struct {
		Commit      string            `json:"commit"`
		SourceBlobs map[string]string `json:"source_blobs"`
	} `json:"middleware"`
	StateMapping []struct {
		TrueNAS  string `json:"truenas"`
		Provider string `json:"provider"`
	} `json:"state_mapping"`
	UnknownStateProvider string   `json:"unknown_state_provider"`
	ActiveWorkloadFields []string `json:"active_workload_fields"`
	ComposeTimeout       int      `json:"compose_action_timeout_seconds"`
	DeleteContract       struct {
		Method                           string `json:"method"`
		RemoveImages                     bool   `json:"remove_images"`
		RemoveIXVolumes                  bool   `json:"remove_ix_volumes"`
		ComposeDownRemovesComposeVolumes bool   `json:"truenas_compose_down_removes_compose_volumes"`
		IXVolumesRemovedOnlyWhenAsked    bool   `json:"ix_volume_datasets_removed_only_when_requested"`
	} `json:"delete_contract"`
}

func loadIXAppsContract(t *testing.T) ixAppsContract {
	t.Helper()
	data, err := os.ReadFile(ixAppsContractFixture)
	if err != nil {
		t.Fatal(err)
	}
	var contract ixAppsContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.SchemaVersion != 1 {
		t.Fatalf("unsupported fixture schema: %d", contract.SchemaVersion)
	}
	if contract.ProfileID != "truenas-scale-25.04.1-apps" {
		t.Fatalf("unexpected source profile: %q", contract.ProfileID)
	}
	if contract.Middleware.Commit != "74ab5a2d373be4097dece257d00e1086376333ba" {
		t.Fatalf("unexpected middleware source commit: %q", contract.Middleware.Commit)
	}
	return contract
}

func TestIXDerivedAppsStateContract(t *testing.T) {
	contract := loadIXAppsContract(t)
	for _, tc := range contract.StateMapping {
		if got := mapState(tc.TrueNAS); string(got) != tc.Provider {
			t.Fatalf("source-derived state mapping %q: got %q want %q", tc.TrueNAS, got, tc.Provider)
		}
	}
	if got := mapState("UPSTREAM-UNKNOWN-STATE"); string(got) != contract.UnknownStateProvider {
		t.Fatalf("unknown state must remain fail-closed: got %q want %q", got, contract.UnknownStateProvider)
	}

	wantFields := []string{"containers", "used_ports", "container_details", "volumes", "images", "networks"}
	if !reflect.DeepEqual(contract.ActiveWorkloadFields, wantFields) {
		t.Fatalf("upstream observed-workload fixture drifted: got %#v want %#v", contract.ActiveWorkloadFields, wantFields)
	}
	if contract.ComposeTimeout != 1200 {
		t.Fatalf("unexpected observed TrueNAS Compose timeout: %d", contract.ComposeTimeout)
	}
}

func TestIXDerivedAppsDeleteContract(t *testing.T) {
	contract := loadIXAppsContract(t)
	var method string
	var params any
	caller := &tnclient.MockClient{
		CallAndWaitFunc: func(_ context.Context, gotMethod string, gotParams any) (json.RawMessage, error) {
			method, params = gotMethod, gotParams
			return nil, nil
		},
	}
	store := New(nil, caller)
	if err := store.DeleteApp(context.Background(), "garm-controller-runner-1"); err != nil {
		t.Fatal(err)
	}
	if method != contract.DeleteContract.Method {
		t.Fatalf("delete method: got %q want %q", method, contract.DeleteContract.Method)
	}
	list, ok := params.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("unexpected delete params: %#v", params)
	}
	opts, ok := list[1].(map[string]any)
	if !ok {
		t.Fatalf("unexpected delete options: %#v", list[1])
	}
	if opts["remove_images"] != contract.DeleteContract.RemoveImages || opts["remove_ix_volumes"] != contract.DeleteContract.RemoveIXVolumes {
		t.Fatalf("provider delete options drifted from source-derived fixture: %#v", opts)
	}
	if !contract.DeleteContract.ComposeDownRemovesComposeVolumes || !contract.DeleteContract.IXVolumesRemovedOnlyWhenAsked {
		t.Fatal("fixture must preserve the distinction between Compose-volume teardown and ixVolume dataset deletion")
	}

	// Provider StateStopping remains provider-side fail-closed protection even
	// though the 25.04.1 query-derived stable App state set does not include it.
	if provider.StateStopping != "STOPPING" {
		t.Fatalf("unexpected provider stopping sentinel: %q", provider.StateStopping)
	}
}
