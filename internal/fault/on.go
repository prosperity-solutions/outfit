//go:build faultinject

package fault

import (
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
)

var faultCount atomic.Int64

// Point is a numbered kill point. With OUTFIT_FAULT_AT=N the process is killed (SIGKILL: no
// deferred function runs, no buffer is flushed, like power loss) when the Nth point is
// reached; OUTFIT_FAULT_LOG=<file> appends every point's name, so a test can learn how
// many there are and where. Only a binary built with the faultinject tag has this.
func Point(name string) {
	n := faultCount.Add(1)
	if path := os.Getenv("OUTFIT_FAULT_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString(strconv.FormatInt(n, 10) + " " + name + "\n")
			_ = f.Close()
		}
	}
	if at, _ := strconv.ParseInt(os.Getenv("OUTFIT_FAULT_AT"), 10, 64); at > 0 && n == at {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
}
