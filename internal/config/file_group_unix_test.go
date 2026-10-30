//go:build !windows

package config

import (
	"os"
	"syscall"
	"testing"
)

func fileGIDForTest(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return int(info.Sys().(*syscall.Stat_t).Gid)
}
