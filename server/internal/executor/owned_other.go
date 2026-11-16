//go:build !linux && !darwin

package executor

import (
	"opsview/internal/security"
	"os"
)

func writeOwnedAtomic(path string, b []byte, mode os.FileMode) error {
	return security.WriteAtomic(path, b, mode)
}
