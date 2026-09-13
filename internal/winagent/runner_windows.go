package winagent

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A Job Object kills the complete process tree on cancellation, disconnect or
// Agent exit. The wrapper waits on stdin until assignment, so user code cannot
// create a child before the Job Object is installed.
func runPowerShell(ctx context.Context, script string, stdout, stderr io.Writer) (int, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 125, err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return 125, err
	}
	encodedScript := base64.StdEncoding.EncodeToString([]byte(script))
	wrapper := `[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
if ([Console]::ReadLine() -ne 'start') { exit 125 }
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$global:LASTEXITCODE = 0
try {
  & ([ScriptBlock]::Create([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd()))))
  if (-not $?) { exit 1 }
  exit $global:LASTEXITCODE
} catch { [Console]::Error.WriteLine($_.ToString()); exit 1 }
`
	units := utf16.Encode([]rune(wrapper))
	bytes := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(bytes[i*2:], u)
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 130) }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 125, err
	}
	defer stdin.Close()
	if err = cmd.Start(); err != nil {
		return 125, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return 125, fmt.Errorf("cannot contain PowerShell process: %w", err)
	}
	if ctx.Err() != nil {
		windows.TerminateJobObject(job, 130)
		cmd.Process.Kill()
		cmd.Wait()
		return 124, ctx.Err()
	}
	if _, err = io.WriteString(stdin, "start\n"+encodedScript); err != nil {
		windows.TerminateJobObject(job, 125)
	}
	stdin.Close()
	waitErr := cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		code = 125
	}
	return code, waitErr
}
