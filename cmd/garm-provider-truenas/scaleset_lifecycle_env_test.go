package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
	garmParams "github.com/cloudbase/garm-provider-common/params"
)

func TestScaleSetLifecyclePoolPlaceholderOnlyCoversInstanceLifecycle(t *testing.T) {
	lifecycle := []commonExecution.ExecutionCommand{
		commonExecution.DeleteInstanceCommand,
		commonExecution.GetInstanceCommand,
		commonExecution.StartInstanceCommand,
		commonExecution.StopInstanceCommand,
	}
	for _, command := range lifecycle {
		t.Run(string(command), func(t *testing.T) {
			t.Setenv("GARM_COMMAND", string(command))
			t.Setenv("GARM_POOL_ID", "")
			ensureScaleSetLifecyclePoolPlaceholder()
			if got := os.Getenv("GARM_POOL_ID"); got != scaleSetLifecyclePoolPlaceholder {
				t.Fatalf("expected lifecycle placeholder, got %q", got)
			}
		})
	}

	strict := []commonExecution.ExecutionCommand{
		commonExecution.CreateInstanceCommand,
		commonExecution.ListInstancesCommand,
		commonExecution.RemoveAllInstancesCommand,
		commonExecution.GetVersionCommand,
		commonExecution.ValidatePoolInfoCommand,
	}
	for _, command := range strict {
		t.Run(string(command), func(t *testing.T) {
			t.Setenv("GARM_COMMAND", string(command))
			t.Setenv("GARM_POOL_ID", "")
			ensureScaleSetLifecyclePoolPlaceholder()
			if got := os.Getenv("GARM_POOL_ID"); got != "" {
				t.Fatalf("command %s must remain strict, got pool placeholder %q", command, got)
			}
		})
	}
}

func TestScaleSetLifecyclePoolPlaceholderPreservesRealPoolID(t *testing.T) {
	t.Setenv("GARM_COMMAND", string(commonExecution.DeleteInstanceCommand))
	t.Setenv("GARM_POOL_ID", "real-pool-id")
	ensureScaleSetLifecyclePoolPlaceholder()
	if got := os.Getenv("GARM_POOL_ID"); got != "real-pool-id" {
		t.Fatalf("real pool ID must not be overwritten, got %q", got)
	}
}

func TestRunCLIAllowsScaleSetGetWithMissingPoolID(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)

	bootstrap := validBootstrap()
	input, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	withStdin(t, input)
	t.Setenv("GARM_COMMAND", string(commonExecution.CreateInstanceCommand))

	var createOut, createErr bytes.Buffer
	if code := runCLI(context.Background(), &createOut, &createErr); code != 0 {
		t.Fatalf("setup create failed with exit %d: %s", code, createErr.String())
	}
	var created garmParams.ProviderInstance
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GARM_COMMAND", string(commonExecution.GetInstanceCommand))
	t.Setenv("GARM_POOL_ID", "")
	t.Setenv("GARM_INSTANCE_ID", created.ProviderID)
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code != 0 {
		t.Fatalf("GetInstance with missing Scale Set pool ID failed with exit %d: %s", code, stderr.String())
	}
}

func TestRunCLIStillRejectsListWithMissingPoolID(t *testing.T) {
	cfgPath := writeMockConfig(t)
	setBaseEnvironment(t, cfgPath)
	t.Setenv("GARM_COMMAND", string(commonExecution.ListInstancesCommand))
	t.Setenv("GARM_POOL_ID", "")
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), &stdout, &stderr); code == 0 {
		t.Fatal("ListInstances unexpectedly accepted an empty pool ID")
	}
	if !strings.Contains(stderr.String(), "missing pool ID") {
		t.Fatalf("expected strict missing-pool validation, got %s", stderr.String())
	}
}
