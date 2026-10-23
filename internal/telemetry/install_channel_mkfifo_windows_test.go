//go:build windows

package telemetry

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs on windows") }
