package truenasstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
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

func TestContainerBootstrapDownloadsVerifiedPayloadAndCleansCredentials(t *testing.T) {
	runnerHome := filepath.Join(t.TempDir(), "runner")
	jitDir := filepath.Join(t.TempDir(), "jit")
	archivePath := filepath.Join(t.TempDir(), "runner.tar.gz")
	tokenMarker := filepath.Join(t.TempDir(), "token-marker")
	archive := testRunnerArchive(t, tokenMarker)
	checksum := fmt.Sprintf("%x", sha256.Sum256(archive))

	const token = "stage4-test-token"
	var mu sync.Mutex
	var statuses []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runner.tar.gz" {
			_, _ = w.Write(archive)
			return
		}
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

	cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapRuntimeCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN="+token,
		"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
		"GARM_RUNNER_FILENAME=runner.tar.gz",
		"GARM_RUNNER_SHA256="+checksum,
		"GARM_BOOTSTRAP_RUNNER_HOME="+runnerHome,
		"GARM_BOOTSTRAP_JIT_DIR="+jitDir,
		"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+archivePath,
		"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME=",
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
	if _, err := os.Stat(filepath.Join(runnerHome, "externals", "node24", "bin", "node")); err != nil {
		t.Fatalf("verified runner payload was not extracted: %v", err)
	}
	for _, path := range []string{
		filepath.Join(runnerHome, ".runner"),
		filepath.Join(runnerHome, ".credentials"),
		filepath.Join(runnerHome, ".credentials_rsaparams"),
		filepath.Join(jitDir, ".runner"),
		filepath.Join(jitDir, ".credentials"),
		filepath.Join(jitDir, ".credentials_rsaparams"),
		archivePath,
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("transient bootstrap artifact survived exit: %s", path)
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
	runnerHome := filepath.Join(t.TempDir(), "runner")
	jitDir := filepath.Join(t.TempDir(), "jit")
	archivePath := filepath.Join(t.TempDir(), "runner.tar.gz")
	archive := testRunnerArchive(t, filepath.Join(t.TempDir(), "marker"))
	checksum := fmt.Sprintf("%x", sha256.Sum256(archive))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/runner.tar.gz":
			_, _ = w.Write(archive)
		case strings.Contains(r.URL.Path, "/metadata/"):
			http.Error(w, "expired", http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", containerBootstrapRuntimeCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN=expired-test-token",
		"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
		"GARM_RUNNER_FILENAME=runner.tar.gz",
		"GARM_RUNNER_SHA256="+checksum,
		"GARM_BOOTSTRAP_RUNNER_HOME="+runnerHome,
		"GARM_BOOTSTRAP_JIT_DIR="+jitDir,
		"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+archivePath,
		"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME=",
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("metadata authentication failure unexpectedly succeeded: %s", output)
	}
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		if _, err := os.Lstat(filepath.Join(runnerHome, name)); !os.IsNotExist(err) {
			t.Fatalf("credential link %s survived failed bootstrap", name)
		}
		if _, err := os.Lstat(filepath.Join(jitDir, name)); !os.IsNotExist(err) {
			t.Fatalf("credential %s survived failed bootstrap", name)
		}
	}
}

func TestContainerBootstrapRejectsRunnerChecksumMismatch(t *testing.T) {
	archive := testRunnerArchive(t, filepath.Join(t.TempDir(), "marker"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runner.tar.gz" {
			_, _ = w.Write(archive)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapRuntimeCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN=test-token",
		"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
		"GARM_RUNNER_FILENAME=runner.tar.gz",
		"GARM_RUNNER_SHA256="+strings.Repeat("0", 64),
		"GARM_BOOTSTRAP_RUNNER_HOME="+filepath.Join(t.TempDir(), "runner"),
		"GARM_BOOTSTRAP_JIT_DIR="+filepath.Join(t.TempDir(), "jit"),
		"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+filepath.Join(t.TempDir(), "runner.tar.gz"),
		"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME=",
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("checksum mismatch unexpectedly succeeded: %s", output)
	}
}

// writePrestagedRunner materializes a fake image-resident runner tree whose
// Runner.Listener reports the supplied version.
func writePrestagedRunner(t *testing.T, home, version, tokenMarker string) {
	t.Helper()
	files := map[string]string{
		"run.sh":                    fmt.Sprintf("#!/bin/bash\nprintf '%%s' \"${GARM_INSTANCE_TOKEN-unset}\" > %q\nsleep 0.2\n", tokenMarker),
		"bin/Runner.Listener":       fmt.Sprintf("#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then printf '%s\\n'; fi\n", version),
		"externals/node24/bin/node": "#!/bin/sh\nexit 0\n",
	}
	for name, body := range files {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func prestagedBootstrapServer(t *testing.T, archive []byte, token string, archiveRequests *int, statuses *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runner.tar.gz" {
			mu.Lock()
			*archiveRequests++
			mu.Unlock()
			_, _ = w.Write(archive)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/metadata/credentials/runner":
			_, _ = io.WriteString(w, `{"agentId":43,"workFolder":"_work"}`)
		case "/metadata/credentials/credentials":
			_, _ = io.WriteString(w, `{"scheme":"OAuth"}`)
		case "/metadata/credentials/credentials_rsaparams":
			_, _ = io.WriteString(w, `{"d":"test"}`)
		case "/callback/status":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			*statuses = append(*statuses, string(body))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/callback/system-info/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestContainerBootstrapReusesMatchingImageResidentRunner(t *testing.T) {
	prestaged := filepath.Join(t.TempDir(), "image-runner")
	downloadHome := filepath.Join(t.TempDir(), "download-runner")
	jitDir := filepath.Join(t.TempDir(), "jit")
	tokenMarker := filepath.Join(t.TempDir(), "token-marker")
	writePrestagedRunner(t, prestaged, "2.336.0", tokenMarker)
	archive := testRunnerArchive(t, filepath.Join(t.TempDir(), "unused-marker"))

	const token = "prestaged-test-token"
	var mu sync.Mutex
	var statuses []string
	archiveRequests := 0
	server := prestagedBootstrapServer(t, archive, token, &archiveRequests, &statuses, &mu)
	defer server.Close()

	cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapRuntimeCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN="+token,
		"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
		"GARM_RUNNER_FILENAME=actions-runner-linux-x64-2.336.0.tar.gz",
		"GARM_RUNNER_SHA256="+strings.Repeat("0", 64),
		"GARM_BOOTSTRAP_RUNNER_HOME="+downloadHome,
		"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME="+prestaged,
		"GARM_BOOTSTRAP_JIT_DIR="+jitDir,
		"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+filepath.Join(t.TempDir(), "runner.tar.gz"),
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
		"GARM_BOOTSTRAP_READY_DELAY_SECONDS=0.05",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prestaged bootstrap failed: %v\n%s", err, output)
	}
	if archiveRequests != 0 {
		t.Fatalf("image-resident runner matched but archive was downloaded %d time(s)", archiveRequests)
	}
	if _, err := os.Stat(downloadHome); !os.IsNotExist(err) {
		t.Fatalf("download runner home must not be created when reusing the image-resident runner")
	}
	marker, err := os.ReadFile(tokenMarker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(marker); got != "unset" {
		t.Fatalf("runner child inherited bootstrap token: %q", got)
	}
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		if _, err := os.Lstat(filepath.Join(prestaged, name)); !os.IsNotExist(err) {
			t.Fatalf("credential link %s survived exit in image-resident runner home", name)
		}
		if _, err := os.Lstat(filepath.Join(jitDir, name)); !os.IsNotExist(err) {
			t.Fatalf("credential %s survived exit", name)
		}
	}
	for _, required := range []string{"run.sh", "bin/Runner.Listener", "externals/node24/bin/node"} {
		if _, err := os.Stat(filepath.Join(prestaged, required)); err != nil {
			t.Fatalf("image-resident runner file %s was removed: %v", required, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(statuses, "\n")
	if !strings.Contains(joined, "using image-resident runner payload") || !strings.Contains(joined, `"status":"idle"`) {
		t.Fatalf("expected image-resident installing and idle callbacks, got %s", joined)
	}
	if strings.Contains(joined, "downloading verified runner payload") || strings.Contains(joined, token) {
		t.Fatalf("unexpected download callback or token leak: %s", joined)
	}
}

func TestContainerBootstrapFallsBackToVerifiedDownloadOnVersionMismatch(t *testing.T) {
	prestaged := filepath.Join(t.TempDir(), "image-runner")
	downloadHome := filepath.Join(t.TempDir(), "download-runner")
	tokenMarker := filepath.Join(t.TempDir(), "token-marker")
	writePrestagedRunner(t, prestaged, "2.335.9", filepath.Join(t.TempDir(), "unused-marker"))
	archive := testRunnerArchive(t, tokenMarker)
	checksum := fmt.Sprintf("%x", sha256.Sum256(archive))

	const token = "fallback-test-token"
	var mu sync.Mutex
	var statuses []string
	archiveRequests := 0
	server := prestagedBootstrapServer(t, archive, token, &archiveRequests, &statuses, &mu)
	defer server.Close()

	cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapRuntimeCommand())
	cmd.Env = append(os.Environ(),
		"GARM_CALLBACK_URL="+server.URL+"/callback",
		"GARM_METADATA_URL="+server.URL+"/metadata",
		"GARM_INSTANCE_TOKEN="+token,
		"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
		"GARM_RUNNER_FILENAME=actions-runner-linux-x64-2.336.0.tar.gz",
		"GARM_RUNNER_SHA256="+checksum,
		"GARM_BOOTSTRAP_RUNNER_HOME="+downloadHome,
		"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME="+prestaged,
		"GARM_BOOTSTRAP_JIT_DIR="+filepath.Join(t.TempDir(), "jit"),
		"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+filepath.Join(t.TempDir(), "runner.tar.gz"),
		"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
		"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
		"GARM_BOOTSTRAP_READY_DELAY_SECONDS=0.05",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback bootstrap failed: %v\n%s", err, output)
	}
	if archiveRequests != 1 {
		t.Fatalf("version mismatch must fall back to exactly one verified download, got %d", archiveRequests)
	}
	if _, err := os.Stat(filepath.Join(downloadHome, "externals", "node24", "bin", "node")); err != nil {
		t.Fatalf("verified runner payload was not extracted on fallback: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(statuses, "\n"), "downloading verified runner payload") {
		t.Fatalf("expected download callback on fallback, got %v", statuses)
	}
}

func TestContainerBootstrapFallsBackToVerifiedDownloadOnDanglingCredentialSymlink(t *testing.T) {
	for _, targetName := range []string{".runner", ".credentials", ".credentials_rsaparams"} {
		t.Run(targetName, func(t *testing.T) {
			prestaged := filepath.Join(t.TempDir(), "image-runner")
			downloadHome := filepath.Join(t.TempDir(), "download-runner")
			tokenMarker := filepath.Join(t.TempDir(), "token-marker")
			writePrestagedRunner(t, prestaged, "2.336.0", filepath.Join(t.TempDir(), "unused-marker"))

			// Create a dangling symlink targeting a non-existent file
			danglingTarget := filepath.Join(t.TempDir(), "nonexistent-target")
			if err := os.Symlink(danglingTarget, filepath.Join(prestaged, targetName)); err != nil {
				t.Fatal(err)
			}

			archive := testRunnerArchive(t, tokenMarker)
			checksum := fmt.Sprintf("%x", sha256.Sum256(archive))

			const token = "dangling-symlink-test-token"
			var mu sync.Mutex
			var statuses []string
			archiveRequests := 0
			server := prestagedBootstrapServer(t, archive, token, &archiveRequests, &statuses, &mu)
			defer server.Close()

			cmd := exec.CommandContext(context.Background(), "bash", "-c", containerBootstrapRuntimeCommand())
			cmd.Env = append(os.Environ(),
				"GARM_CALLBACK_URL="+server.URL+"/callback",
				"GARM_METADATA_URL="+server.URL+"/metadata",
				"GARM_INSTANCE_TOKEN="+token,
				"GARM_RUNNER_DOWNLOAD_URL="+server.URL+"/runner.tar.gz",
				"GARM_RUNNER_FILENAME=actions-runner-linux-x64-2.336.0.tar.gz",
				"GARM_RUNNER_SHA256="+checksum,
				"GARM_BOOTSTRAP_RUNNER_HOME="+downloadHome,
				"GARM_BOOTSTRAP_PRESTAGED_RUNNER_HOME="+prestaged,
				"GARM_BOOTSTRAP_JIT_DIR="+filepath.Join(t.TempDir(), "jit"),
				"GARM_BOOTSTRAP_RUNNER_ARCHIVE="+filepath.Join(t.TempDir(), "runner.tar.gz"),
				"GARM_BOOTSTRAP_MAX_ATTEMPTS=1",
				"GARM_BOOTSTRAP_RETRY_DELAY_SECONDS=0",
				"GARM_BOOTSTRAP_READY_DELAY_SECONDS=0.05",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bootstrap failed when falling back from dangling symlink %s: %v\n%s", targetName, err, output)
			}
			if archiveRequests != 1 {
				t.Fatalf("dangling symlink %s must fall back to exactly one verified download, got %d", targetName, archiveRequests)
			}
			if _, err := os.Stat(filepath.Join(downloadHome, "externals", "node24", "bin", "node")); err != nil {
				t.Fatalf("verified runner payload was not extracted on fallback: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(strings.Join(statuses, "\n"), "downloading verified runner payload") {
				t.Fatalf("expected download callback on fallback from dangling symlink, got %v", statuses)
			}
		})
	}
}

func TestContainerBootstrapScriptDoesNotEmbedSecretValues(t *testing.T) {
	script := containerBootstrapRuntimeCommand()
	for _, forbidden := range []string{"stage4-test-token", "ephemeral-test-token"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("bootstrap script contains secret fixture %q", forbidden)
		}
	}
	for _, required := range []string{
		"sha256sum -c",
		"externals/node24/bin/node",
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

func testRunnerArchive(t *testing.T, tokenMarker string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	files := []struct {
		name string
		mode int64
		body string
	}{
		{
			name: "run.sh",
			mode: 0o755,
			body: fmt.Sprintf("#!/bin/bash\nprintf '%%s' \"${GARM_INSTANCE_TOKEN-unset}\" > %q\nsleep 0.2\n", tokenMarker),
		},
		{name: "bin/Runner.Listener", mode: 0o755, body: "runner-listener-test\n"},
		{name: "externals/node24/bin/node", mode: 0o755, body: "node24-test\n"},
	}
	for _, file := range files {
		hdr := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
