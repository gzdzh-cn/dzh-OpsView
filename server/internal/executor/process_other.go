//go:build !linux && !darwin

package executor

import "os/exec"

func isolateProcess(cmd *exec.Cmd) {}
