//go:build !windows

package winagent

import "errors"

func scheduleExecutableReplacement(string, string, string, int) error {
	return errors.New("自动升级仅支持Windows")
}
