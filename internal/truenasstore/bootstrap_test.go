package truenasstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContainerBootstrapUsesPinnedAssetsAndCleansCredentials(t *testing.T) {
	assets := t.TempDir()
	runnerHome := filepath.Join(t.TempDir(), "runner")
	tokenMarker := filepath.Join(t.TempDir(), "token-marker")
	runScript := fmt.Sprintf("#!/bin/bash\nprintf '%%s' \"${GARM_INSTANCE_TOKEN-unset}\" > %q\nsleep 0.2\n", tokenMarker)
	if err := os.WriteFile(filepath.Join(assets, "run.sh"), []byte(runScript), 0o755); err != nil {
		t.Fatal(err)
	}

	const token = "stage4-test-token"
	var mu sync.Mutex
	var statuses []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/metadata/credentials/runner":
			_, _ = io.WriteString(w, `{"agentId":42,"workFolder":"_work"}`)
		case "/metadata/credentials/credentials":
			_, _ = io.WriteString(w, `{"scheme":"OAuth"}`)
		case "/metadata/credentials/credentials_rsaparams":
			_, _ = io.WriteString(w, `{"d":"test"}`)
		case "/callback/status":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			statuses = append(statuses, string(body))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/callback/system-info/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN="+token,
		"RUNNER_ASSETS_DIR="+assets,
		"RUNNER_HOME="+runnerHome,
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
		"GARM_BOOTSTRAP_READY_DELAY_SECONDS=0.05",
		"TEST_TOKEN_MARKER="+tokenMarker,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, output)
	}

	marker, err := os.ReadFile(tokenMarker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(marker); got != "unset" {
		t.Fatalf("runner child inherited bootstrap token: %q", got)
	}
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		if _, err := os.Stat(filepath.Join(runnerHome, name)); !os.IsNotExist(err) {
			t.Fatalf("credential %s survived bootstrap exit", name)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(statuses, "\n")
	if !strings.Contains(joined, `"status":"installing"`) || !strings.Contains(joined, `"status":"idle"`) {
		t.Fatalf("expected installing and idle callbacks, got %s", joined)
	}
	if strings.Contains(joined, token) {
		t.Fatal("bootstrap token leaked into callback payload")
	}
}

func TestContainerBootstrapFailsClosedOnMetadataAuthFailure(t *testing.T) {
	assets := t.TempDir()
	runnerHome := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(filepath.Join(assets, "run.sh"), []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/metadata/") {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", containerBootstrapCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN=expired-test-token",
		"RUNNER_ASSETS_DIR="+assets,
		"RUNNER_HOME="+runnerHome,
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("metadata authentication failure unexpectedly succeeded: %s", output)
	}
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		if _, err := os.Stat(filepath.Join(runnerHome, name)); !os.IsNotExist(err) {
			t.Fatalf("credential %s survived failed bootstrap", name)
		}
	}
}

func TestContainerBootstrapScriptDoesNotEmbedSecretValues(t *testing.T) {
	script := containerBootstrapCommand()
	for _, forbidden := range []string{"stage4-test-token", "ephemeral-test-token"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("bootstrap script contains secret fixture %q", forbidden)
		}
	}
	for _, required := range []string{
		"credentials/runner",
		"credentials/credentials",
		"credentials/credentials_rsaparams",
		"env -u GARM_INSTANCE_TOKEN ./run.sh",
		"trap cleanup_credentials EXIT",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("bootstrap script missing required contract fragment %q", required)
		}
	}
}
