package main

import (
	"os"
	"strings"

	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
)

const scaleSetLifecyclePoolPlaceholder = "garm-scaleset-lifecycle"

// ensureScaleSetLifecyclePoolPlaceholder is a narrow compatibility shim for
// GARM v0.2.1 scale-set instance lifecycle calls. That GARM lineage leaves
// ProviderBaseParams.PoolInfo.ID empty for Get/Delete/Start/Stop, while the
// shared v0.1.1 provider environment parser requires GARM_POOL_ID before it can
// dispatch those calls. The TrueNAS provider methods themselves do not receive
// or use a pool ID for these operations; ownership remains enforced by the
// controller-owned TrueNAS app identity.
//
// CreateInstance and ListInstances are deliberately excluded because pool
// identity is meaningful to those operations and must continue to fail closed
// when absent.
func ensureScaleSetLifecyclePoolPlaceholder() {
	if strings.TrimSpace(os.Getenv("GARM_POOL_ID")) != "" {
		return
	}

	switch commonExecution.ExecutionCommand(os.Getenv("GARM_COMMAND")) {
	case commonExecution.DeleteInstanceCommand,
		commonExecution.GetInstanceCommand,
		commonExecution.StartInstanceCommand,
		commonExecution.StopInstanceCommand:
		_ = os.Setenv("GARM_POOL_ID", scaleSetLifecyclePoolPlaceholder)
	}
}
