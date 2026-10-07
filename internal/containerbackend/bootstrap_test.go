package containerbackend

import (
	"strings"
	"testing"
)

func TestBootstrapPlanStagesOnlyNonSecretFiles(t *testing.T) {
	in := bootstrap()
	plan := buildBootstrapPlan(in)

	if plan.Init != BootstrapInitCommand {
		t.Fatalf("unexpected bootstrap init: %q", plan.Init)
	}
	if plan.FinalInit != DefaultInit {
		t.Fatalf("unexpected final init: %q", plan.FinalInit)
	}
	if _, ok := plan.InitEnv["GARM_INSTANCE_TOKEN"]; ok {
		t.Fatal("bootstrap credential must not appear in container.create init environment")
	}
	if plan.InitEnv["RUNNER_ALLOW_RUNASROOT"] != "1" {
		t.Fatal("system-container runner must explicitly allow container-root execution")
	}
	if len(plan.FinalEnv) != 0 {
		t.Fatalf("final persisted environment must be empty, got %#v", plan.FinalEnv)
	}
	if planContainsToken(plan, in.Token) {
		t.Fatal("one-time bootstrap token leaked into a staged file")
	}
	if len(plan.Files) != 2 {
		t.Fatalf("expected two staged non-secret bootstrap files, got %d", len(plan.Files))
	}
	for _, file := range plan.Files {
		if file.ContainsSecret {
			t.Fatalf("staged file unexpectedly marked secret: %s", file.Path)
		}
		if file.Mode != 0o755 {
			t.Fatalf("unexpected mode for %s: %#o", file.Path, file.Mode)
		}
		if len(file.SHA256) != 64 {
			t.Fatalf("missing exact staged-file digest for %s: %q", file.Path, file.SHA256)
		}
	}
}

func TestContainerInitWrapperScrubsEnvironmentBeforeSystemd(t *testing.T) {
	if !strings.Contains(containerInitWrapper, "exec "+BootstrapRunnerScriptPath) {
		t.Fatal("wrapper does not launch the runner bootstrap child")
	}
	for _, marker := range []string{InitWrapperMarkerPath, BootstrapChildMarkerPath} {
		if !strings.Contains(containerInitWrapper, marker) {
			t.Fatalf("wrapper does not emit execution marker %s", marker)
		}
	}
	for _, name := range []string{
		"GARM_CALLBACK_URL",
		"GARM_METADATA_URL",
		"GARM_INSTANCE_TOKEN",
		"GARM_RUNNER_DOWNLOAD_URL",
		"GARM_RUNNER_FILENAME",
		"GARM_RUNNER_SHA256",
		"RUNNER_ALLOW_RUNASROOT",
	} {
		if !strings.Contains(containerInitWrapper, "unset "+name) {
			t.Fatalf("wrapper fails to scrub %s before systemd exec", name)
		}
	}
	if !strings.Contains(containerInitWrapper, "exec "+DefaultInit) {
		t.Fatal("wrapper does not replace itself with the stock init process")
	}
}

func TestBootstrapPlanDoesNotPersistTemporaryInitOrEnvironment(t *testing.T) {
	plan := buildBootstrapPlan(bootstrap())
	if plan.FinalInit == plan.Init {
		t.Fatal("temporary bootstrap init must not remain in desired state")
	}
	if tokenPresent(plan.FinalEnv, bootstrap().Token) {
		t.Fatal("bootstrap token remains in final desired environment")
	}
}
