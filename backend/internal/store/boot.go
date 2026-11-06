package store

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type bootIdentity struct {
	host string
	boot string
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func currentBootIdentity() bootIdentity {
	if runtime.GOOS != "darwin" {
		return bootIdentity{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	read := func(key string) string {
		out, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", key).Output()
		if err != nil {
			return ""
		}
		value := strings.ToLower(strings.TrimSpace(string(out)))
		if !uuidPattern.MatchString(value) {
			return ""
		}
		return value
	}
	host, boot := hostMachineID(), read("kern.bootsessionuuid")
	if host == "" || boot == "" {
		return bootIdentity{}
	}
	return bootIdentity{host: host, boot: boot}
}
