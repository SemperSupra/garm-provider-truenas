package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudbase/garm-provider-common/execution"
	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
)

var signals = []os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()
	os.Exit(runCLI(ctx, os.Stdout, os.Stderr))
}

func runCLI(ctx context.Context, stdout, stderr io.Writer) int {
	executionEnv, err := execution.GetEnvironment()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	prov, err := newExternalProvider(executionEnv.ProviderConfigFile, executionEnv.ControllerID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return commonExecution.ResolveErrorToExitCode(err)
	}

	result, err := executionEnv.Run(ctx, prov)
	if err != nil {
		fmt.Fprintf(stderr, "failed to run command: %v\n", err)
		return commonExecution.ResolveErrorToExitCode(err)
	}
	if result != "" {
		fmt.Fprint(stdout, result)
	}
	return 0
}
