package containerbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
	"github.com/SemperSupra/garm-provider-truenas/internal/truenasstore"
)

const (
	DefaultInit               = "/sbin/init"
	BootstrapInitCommand      = "/bin/sh /usr/local/bin/garm-container-init"
	BootstrapRunnerScriptPath = "/usr/local/bin/garm-runner-bootstrap"
	BootstrapInitScriptPath   = "/usr/local/bin/garm-container-init"
	BootstrapTokenPath        = "/var/lib/garm-container/bootstrap-instance-token"
	InitWrapperMarkerPath     = "/var/lib/garm-container/init-wrapper-executed"
	BootstrapChildMarkerPath  = "/var/lib/garm-container/bootstrap-child-started"
)

type StagedFile struct {
	Path           string `json:"path"`
	Mode           int    `json:"mode"`
	Content        string `json:"content"`
	SHA256         string `json:"sha256"`
	ContainsSecret bool   `json:"contains_secret"`
}

type BootstrapPlan struct {
	Init      string
	InitEnv   map[string]string
	Files     []StagedFile
	FinalInit string
	FinalEnv  map[string]string
}

const containerInitWrapper = `#!/bin/sh
set -eu

mkdir -p /var/lib/garm-container
printf 'init-wrapper-v1\\n' > /var/lib/garm-container/init-wrapper-executed
GARM_INSTANCE_TOKEN="$(cat "/var/lib/garm-container/bootstrap-instance-token")"
rm -f "/var/lib/garm-container/bootstrap-instance-token"
export GARM_INSTANCE_TOKEN
(
  printf 'bootstrap-child-v1\\n' > /var/lib/garm-container/bootstrap-child-started
  exec /usr/local/bin/garm-runner-bootstrap
) &
unset GARM_CALLBACK_URL
unset GARM_METADATA_URL
unset GARM_INSTANCE_TOKEN
unset GARM_RUNNER_DOWNLOAD_URL
unset GARM_RUNNER_FILENAME
unset GARM_RUNNER_SHA256
unset RUNNER_ALLOW_RUNASROOT
exec /sbin/init
`

func buildBootstrapPlan(in provider.Bootstrap) BootstrapPlan {
	initial := bootstrapEnv(in)
	delete(initial, "GARM_INSTANCE_TOKEN")
	initial["RUNNER_ALLOW_RUNASROOT"] = "1"

	return BootstrapPlan{
		Init:    BootstrapInitCommand,
		InitEnv: initial,
		Files: []StagedFile{
			stagedFile(BootstrapRunnerScriptPath, 0o755, truenasstore.ContainerBootstrapRuntimeCommand(), false),
			stagedFile(BootstrapInitScriptPath, 0o755, containerInitWrapper, false),
			stagedFile(BootstrapTokenPath, 0o600, in.Token, true),
		},
		FinalInit: DefaultInit,
		FinalEnv:  map[string]string{},
	}
}

func stagedFile(path string, mode int, content string, containsSecret bool) StagedFile {
	sum := sha256.Sum256([]byte(content))
	return StagedFile{
		Path:           path,
		Mode:           mode,
		Content:        content,
		SHA256:         hex.EncodeToString(sum[:]),
		ContainsSecret: containsSecret,
	}
}

func planContainsToken(plan BootstrapPlan, token string) bool {
	if token == "" {
		return false
	}
	for _, file := range plan.Files {
		if !file.ContainsSecret && strings.Contains(file.Content, token) {
			return true
		}
	}
	return false
}
