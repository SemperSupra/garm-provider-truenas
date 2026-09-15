package truenasstore

import (
	"strings"
	"testing"
)

func TestComposeBootstrapEscapesShellDollarsForRuntimeExpansion(t *testing.T) {
	raw := containerBootstrapRuntimeCommand()
	compose := containerBootstrapCommand()

	if raw == compose {
		t.Fatal("Compose bootstrap unexpectedly matches raw shell program")
	}
	if !strings.Contains(raw, "${GARM_INSTANCE_TOKEN:?GARM_INSTANCE_TOKEN is required}") {
		t.Fatal("raw bootstrap lost the runtime token guard")
	}
	if !strings.Contains(compose, "$${GARM_INSTANCE_TOKEN:?GARM_INSTANCE_TOKEN is required}") {
		t.Fatal("Compose bootstrap does not escape braced runtime interpolation")
	}
	if !strings.Contains(compose, "$$GARM_RUNNER_FILENAME") {
		t.Fatal("Compose bootstrap does not escape simple runtime interpolation")
	}
	if strings.Contains(compose, "\"$GARM_INSTANCE_TOKEN\"") {
		t.Fatal("Compose bootstrap contains an unescaped runtime token reference")
	}
	if strings.ReplaceAll(compose, "$$", "$") != raw {
		t.Fatal("Compose escaping does not round-trip to the raw runtime shell program")
	}
}
