package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	garmParams "github.com/cloudbase/garm-provider-common/params"
)

var linuxX64RunnerFilename = regexp.MustCompile(`^actions-runner-linux-x64-(\d+\.\d+\.\d+)\.tar\.gz$`)

// validateRunnerToolContract verifies that GARM still advertises a coherent,
// official GitHub Linux/x64 runner artifact. The TrueNAS provider deliberately
// does not couple this advertised version to its independently pinned runtime
// payload: Manager.Create() continues to install provider.RunnerTool*.
//
// This keeps the provider fail-closed on contract/origin drift without forcing
// a provider release every time GitHub advances the runner version returned by
// the runner-application-downloads API.
func validateRunnerToolContract(bootstrap garmParams.BootstrapInstance) error {
	var selected *garmParams.RunnerApplicationDownload
	for i := range bootstrap.Tools {
		tool := &bootstrap.Tools[i]
		if strings.EqualFold(tool.GetOS(), "linux") && (strings.EqualFold(tool.GetArchitecture(), "x64") || strings.EqualFold(tool.GetArchitecture(), "amd64")) {
			selected = tool
			break
		}
	}
	if selected == nil {
		return errors.New("GARM bootstrap does not contain a Linux/x64 runner tool")
	}
	if selected.GetTempDownloadToken() != "" {
		return errors.New("temporary runner download tokens are not supported by the fixed TrueNAS MVP profile")
	}

	filename := selected.GetFilename()
	match := linuxX64RunnerFilename.FindStringSubmatch(filename)
	if match == nil {
		return fmt.Errorf("runner tool filename %q is not an official Linux/x64 GitHub runner release shape", filename)
	}
	version := match[1]
	expectedURL := fmt.Sprintf("https://github.com/actions/runner/releases/download/v%s/%s", version, filename)
	if selected.GetDownloadURL() != expectedURL {
		return fmt.Errorf("runner tool URL does not match the official GitHub release path for %s", version)
	}

	checksum := strings.TrimSpace(selected.GetSHA256Checksum())
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != 32 || strings.Trim(checksum, "0") == "" {
		return errors.New("runner tool SHA256 is not a valid nonzero 256-bit digest")
	}
	return nil
}
