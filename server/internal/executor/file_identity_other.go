//go:build !linux && !darwin

package executor

import "os"

func fileIdentity(st os.FileInfo) string { return st.Name() }
