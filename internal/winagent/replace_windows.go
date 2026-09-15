//go:build windows

package winagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

const replacementScript = `param(
  [int]$OldProcessId,
  [string]$Target,
  [string]$Staged,
  [string]$LogPath
)
$ErrorActionPreference = 'Stop'
$backup = $Target + '.old'
try {
  Wait-Process -Id $OldProcessId -ErrorAction SilentlyContinue
  for ($attempt = 1; $attempt -le 60; $attempt++) {
    try {
      Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
      Move-Item -LiteralPath $Target -Destination $backup -Force
      Move-Item -LiteralPath $Staged -Destination $Target -Force
      Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
      Add-Content -LiteralPath $LogPath -Encoding UTF8 -Value "$(Get-Date -Format o) 升级成功"
      exit 0
    } catch {
      if (!(Test-Path -LiteralPath $Target) -and (Test-Path -LiteralPath $backup)) {
        Move-Item -LiteralPath $backup -Destination $Target -Force -ErrorAction SilentlyContinue
      }
      Start-Sleep -Milliseconds 250
    }
  }
  throw '等待客户端文件解锁超时'
} catch {
  Add-Content -LiteralPath $LogPath -Encoding UTF8 -Value "$(Get-Date -Format o) 升级失败：$($_.Exception.Message)"
  exit 1
} finally {
  Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue
}
`

func scheduleExecutableReplacement(executable, staged, logPath string, processID int) error {
	script, err := os.CreateTemp("", "aiagent-ssh-client-upgrade-*.ps1")
	if err != nil {
		return err
	}
	scriptPath := script.Name()
	if _, err = script.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		script.Close()
		os.Remove(scriptPath)
		return err
	}
	if _, err = script.WriteString(replacementScript); err != nil {
		script.Close()
		os.Remove(scriptPath)
		return err
	}
	if err = script.Close(); err != nil {
		os.Remove(scriptPath)
		return err
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", scriptPath,
		"-OldProcessId", strconv.Itoa(processID), "-Target", executable, "-Staged", staged, "-LogPath", logPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("启动PowerShell替换进程失败：%w", err)
	}
	return cmd.Process.Release()
}
