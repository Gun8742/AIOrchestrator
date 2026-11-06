//go:build !darwin && !linux

package antigravity

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
