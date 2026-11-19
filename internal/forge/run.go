package forge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// RunExe runs a Windows executable (natively on Windows, through Wine elsewhere)
// and returns its combined output. GUI programs are stopped after timeout.
func RunExe(path string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, path, args...)
	} else {
		wine := FindWine()
		if wine == "" {
			return "", errors.New("Wine is not installed, so .exe files can't run here (Debian/Ubuntu: sudo apt install wine)")
		}
		cmd = exec.CommandContext(ctx, wine, append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), errors.New("stopped after " + timeout.String() + " (program kept running)")
	}
	return buf.String(), err
}
