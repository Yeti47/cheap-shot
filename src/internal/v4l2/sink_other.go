//go:build !linux

package v4l2

import (
	"errors"
	"runtime"
)

// Sink is unavailable outside Linux.
type Sink struct{}

// Open always fails on non-Linux platforms.
func Open(path string) (*Sink, error) {
	return nil, errors.New("v4l2: the /dev/video sink requires Linux, not " + runtime.GOOS)
}

// Write is never reached.
func (s *Sink) Write(frame []byte) error { return errors.New("v4l2: unsupported platform") }

// Close is never reached.
func (s *Sink) Close() error { return nil }
