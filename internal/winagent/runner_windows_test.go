package winagent

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPowerShellReadFileUnicodeAndExit(t *testing.T) {
	for _, tc := range []struct {
		script, want string
		code         int
	}{
		{`Write-Output '中文输出'; [Console]::Error.WriteLine('错误流'); exit 7`, "中文输出", 7},
		{`Get-Content -LiteralPath '` + strings.ReplaceAll(t.TempDir(), "'", "''") + `\missing.log'`, "", 1},
		{strings.Repeat("# padding\n", 5000) + `Write-Output 'long-command'`, "long-command", 0},
	} {
		var out, errOut bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		code, err := runPowerShell(ctx, tc.script, &out, &errOut)
		cancel()
		if code != tc.code || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("code=%d err=%v stdout=%s stderr=%s", code, err, out.String(), errOut.String())
		}
		if tc.code == 7 && !strings.Contains(errOut.String(), "错误流") {
			t.Fatalf("stderr missing: %s", errOut.String())
		}
	}
}

type pidWriter struct {
	ready chan int
	text  strings.Builder
}

func (w *pidWriter) Write(p []byte) (int, error) {
	w.text.Write(p)
	if strings.Contains(w.text.String(), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(w.text.String())); err == nil {
			select {
			case w.ready <- pid:
			default:
			}
		}
	}
	return len(p), nil
}

func TestCancellationKillsChildProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &pidWriter{ready: make(chan int, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPowerShell(ctx, `$p = Start-Process -FilePath "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" -ArgumentList '-NoProfile','-Command','Start-Sleep 60' -WindowStyle Hidden -PassThru; [Console]::WriteLine($p.Id); Start-Sleep 60`, w, io.Discard)
	}()
	var pid int
	select {
	case pid = <-w.ready:
	case <-time.After(15 * time.Second):
		t.Fatal("child did not start")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	defer windows.TerminateProcess(h, 1)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation hung")
	}
	state, err := windows.WaitForSingleObject(h, 3000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("child survived: state=%d err=%v", state, err)
	}
}
