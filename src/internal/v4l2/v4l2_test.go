package v4l2

import "testing"

func TestJPEGDimensions(t *testing.T) {
	// SOI, APP0 (skipped), SOF0 with 480x640, then SOS.
	jpeg := []byte{0xff, 0xd8}
	jpeg = append(jpeg, 0xff, 0xe0, 0x00, 0x04, 0x00, 0x00) // APP0, length 4
	jpeg = append(jpeg, 0xff, 0xc0, 0x00, 0x11, 0x08,
		0x01, 0xe0, // height 480
		0x02, 0x80, // width 640
	)
	w, h, err := jpegDimensions(jpeg)
	if err != nil {
		t.Fatal(err)
	}
	if w != 640 || h != 480 {
		t.Fatalf("got %dx%d, want 640x480", w, h)
	}
}

func TestJPEGDimensionsSkipsDHT(t *testing.T) {
	// A DHT (0xc4) sits inside the SOF marker range but is not a frame header.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xc4, 0x00, 0x05, 0, 0, 0}
	jpeg = append(jpeg, 0xff, 0xc2, 0x00, 0x11, 0x08, 0x00, 0x64, 0x00, 0xc8)
	w, h, err := jpegDimensions(jpeg)
	if err != nil {
		t.Fatal(err)
	}
	if w != 200 || h != 100 {
		t.Fatalf("got %dx%d, want 200x100", w, h)
	}
}

func TestJPEGDimensionsRejectsNonJPEG(t *testing.T) {
	if _, _, err := jpegDimensions([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("want an error for a non-JPEG buffer")
	}
}
