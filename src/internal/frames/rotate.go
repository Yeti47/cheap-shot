package frames

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// RotateJPEG rotates a JPEG clockwise by a supported quarter-turn amount.
func RotateJPEG(frame []byte, degrees int) ([]byte, error) {
	if degrees == 0 {
		return frame, nil
	}
	if degrees != 90 && degrees != 180 && degrees != 270 {
		return nil, fmt.Errorf("frames: unsupported rotation %d degrees", degrees)
	}

	source, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("frames: decode JPEG: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	destinationWidth, destinationHeight := width, height
	if degrees == 90 || degrees == 270 {
		destinationWidth, destinationHeight = height, width
	}
	destination := image.NewRGBA(image.Rect(0, 0, destinationWidth, destinationHeight))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			destinationX, destinationY := x-bounds.Min.X, y-bounds.Min.Y
			switch degrees {
			case 90:
				destinationX, destinationY = height-1-(y-bounds.Min.Y), x-bounds.Min.X
			case 180:
				destinationX, destinationY = width-1-(x-bounds.Min.X), height-1-(y-bounds.Min.Y)
			case 270:
				destinationX, destinationY = y-bounds.Min.Y, width-1-(x-bounds.Min.X)
			}
			destination.Set(destinationX, destinationY, source.At(x, y))
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, destination, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("frames: encode rotated JPEG: %w", err)
	}
	return out.Bytes(), nil
}
