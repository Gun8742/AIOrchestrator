//go:build darwin && cgo

package store

/*
#include <unistd.h>
#include <time.h>
#include <uuid/uuid.h>

static int machine_uuid(unsigned char *out) {
    uuid_t value;
    struct timespec timeout = { .tv_sec = 1, .tv_nsec = 0 };
    if (gethostuuid(value, &timeout) != 0) return -1;
    for (int i = 0; i < 16; i++) out[i] = value[i];
    return 0;
}
*/
import "C"

import "crypto/sha256"
import "encoding/hex"

func hostMachineID() string {
	var raw [16]C.uchar
	if C.machine_uuid(&raw[0]) != 0 {
		return ""
	}
	data := make([]byte, len(raw))
	nonzero := false
	for i, v := range raw {
		data[i] = byte(v)
		nonzero = nonzero || v != 0
	}
	if !nonzero {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
