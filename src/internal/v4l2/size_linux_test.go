//go:build linux

package v4l2

import (
	"testing"
	"unsafe"
)

// The ioctl request number encodes sizeof(struct v4l2_format). If the Go
// struct ever drifts from 208 bytes the kernel would reject the call with
// ENOTTY, so pin it here.
func TestFormatStructSize(t *testing.T) {
	if got := unsafe.Sizeof(v4l2Format{}); got != formatSize {
		t.Fatalf("sizeof(v4l2Format) = %d, want %d", got, formatSize)
	}
	if want := uintptr(0xC0D05605); vidiocSFmt != want {
		t.Fatalf("VIDIOC_S_FMT = %#x, want %#x", uintptr(vidiocSFmt), want)
	}
	if got := (vidiocSFmt >> 16) & 0x3fff; got != formatSize {
		t.Fatalf("ioctl request encodes size %d, struct is %d", got, formatSize)
	}
}
