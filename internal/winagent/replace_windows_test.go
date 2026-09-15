//go:build windows

package winagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleExecutableReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "renamed-client.exe")
	staged := filepath.Join(dir, "staged.exe")
	logPath := filepath.Join(dir, "upgrade.log")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := scheduleExecutableReplacement(target, staged, logPath, 2147483647); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(target)
		logData, logErr := os.ReadFile(logPath)
		if readErr == nil && string(data) == "new" && logErr == nil && strings.Contains(string(logData), "升级成功") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("后台替换未完成，目标=%q，日志=%q", readFileForTest(target), readFileForTest(logPath))
}

func readFileForTest(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
