//go:build linux

// Package v4l2 writes MJPEG frames into a v4l2loopback output device so the
// camera shows up as an ordinary /dev/video* webcam.
package v4l2

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// VIDIOC_S_FMT is _IOWR('V', 5, struct v4l2_format); the 208 in the request
// number is sizeof(struct v4l2_format), which formatSize asserts.
const vidiocSFmt = 0xC0D05605

const (
	bufTypeVideoOutput = 2
	fieldNone          = 1
	pixFmtMJPEG        = 0x47504A4D // 'MJPG'
	defaultSizeImage   = 1 << 20
)

// v4l2Format mirrors struct v4l2_format: a type tag, four bytes of padding
// from the union's 8-byte alignment, and a 200-byte union.
type v4l2Format struct {
	Type uint32
	_    uint32
	Raw  [200]byte
}

// formatSize is what the ioctl request number encodes.
const formatSize = 208

// Sink is an open v4l2loopback output device.
type Sink struct {
	f    *os.File
	path string
	w, h int
}

// Open opens the loopback device for writing.
func Open(path string) (*Sink, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("v4l2: %s is unavailable (load the module first: "+
			"sudo modprobe v4l2loopback exclusive_caps=1 card_label=cheap-shot): %w", path, err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("v4l2: %s is not a character device", path)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("v4l2: open %s: %w", path, err)
	}
	return &Sink{f: f, path: path}, nil
}

// Write pushes one JPEG frame to the device, (re)negotiating the format
// whenever the frame dimensions change.
func (s *Sink) Write(frame []byte) error {
	w, h, err := jpegDimensions(frame)
	if err != nil {
		return err
	}
	if w != s.w || h != s.h {
		if err := s.setFormat(w, h); err != nil {
			return err
		}
		s.w, s.h = w, h
	}
	if _, err := s.f.Write(frame); err != nil {
		return fmt.Errorf("v4l2: write %s: %w", s.path, err)
	}
	return nil
}

// Close releases the device.
func (s *Sink) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

func (s *Sink) setFormat(w, h int) error {
	var f v4l2Format
	f.Type = bufTypeVideoOutput
	// struct v4l2_pix_format: width, height, pixelformat, field,
	// bytesperline, sizeimage, ...
	be := binary.NativeEndian
	be.PutUint32(f.Raw[0:], uint32(w))
	be.PutUint32(f.Raw[4:], uint32(h))
	be.PutUint32(f.Raw[8:], pixFmtMJPEG)
	be.PutUint32(f.Raw[12:], fieldNone)
	be.PutUint32(f.Raw[16:], 0) // bytesperline: 0 for a compressed format
	size := uint32(defaultSizeImage)
	if minimum := uint32(w * h * 3 / 2); minimum > size {
		size = minimum
	}
	be.PutUint32(f.Raw[20:], size)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, s.f.Fd(), vidiocSFmt,
		uintptr(unsafe.Pointer(&f))); errno != 0 {
		return fmt.Errorf("v4l2: VIDIOC_S_FMT on %s: %w", s.path, errno)
	}
	return nil
}
