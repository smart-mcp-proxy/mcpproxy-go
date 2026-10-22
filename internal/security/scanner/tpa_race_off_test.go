//go:build !race

package scanner

// raceDetectorEnabled is true under -race, where wall-clock latency
// assertions are meaningless.
const raceDetectorEnabled = false
