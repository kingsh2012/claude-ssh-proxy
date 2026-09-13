//go:build !windows

package winagent

import (
	"context"
	"errors"
	"io"
)

func runPowerShell(context.Context, string, io.Writer, io.Writer) (int, error) {
	return 125, errors.New("PowerShell Agent requires Windows")
}
