//go:build linux

package v4l2

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
)

// TestSinkAgainstRealDevice exercises the ioctl path against an actual
// v4l2loopback node. It is opt-in because it needs a loopback device that
// nothing else is producing into:
//
//	sudo modprobe v4l2loopback exclusive_caps=1 card_label=cheap-shot
//	CHEAPSHOT_V4L2_TEST_DEVICE=/dev/video10 go test ./src/internal/v4l2/
func TestSinkAgainstRealDevice(t *testing.T) {
	path := os.Getenv("CHEAPSHOT_V4L2_TEST_DEVICE")
	if path == "" {
		t.Skip("set CHEAPSHOT_V4L2_TEST_DEVICE to run this")
	}
	sink, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	img := image.NewGray(image.Rect(0, 0, 640, 480))
	for y := range 480 {
		for x := range 640 {
			img.SetGray(x, y, color.Gray{Y: uint8((x + y) % 256)})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := sink.Write(buf.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if sink.w != 640 || sink.h != 480 {
		t.Fatalf("sink negotiated %dx%d", sink.w, sink.h)
	}
}
