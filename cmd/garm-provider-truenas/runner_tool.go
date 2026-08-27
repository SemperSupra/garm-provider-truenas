package main

import (
	"errors"
	"fmt"
	"strings"

	garmParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

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
	if selected.GetFilename() != provider.RunnerToolFilename {
		return fmt.Errorf("runner tool filename %q does not match tested contract %q", selected.GetFilename(), provider.RunnerToolFilename)
	}
	if selected.GetDownloadURL() != provider.RunnerToolURL {
		return fmt.Errorf("runner tool URL does not match tested %s release", provider.RunnerVersion)
	}
	if !strings.EqualFold(selected.GetSHA256Checksum(), provider.RunnerToolSHA256) {
		return fmt.Errorf("runner tool SHA256 does not match tested %s release", provider.RunnerVersion)
	}
	if selected.GetTempDownloadToken() != "" {
		return errors.New("temporary runner download tokens are not supported by the fixed TrueNAS MVP profile")
	}
	return nil
}
