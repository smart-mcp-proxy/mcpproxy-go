//go:build race

package server

func init() {
	raceEnabled = true
}
