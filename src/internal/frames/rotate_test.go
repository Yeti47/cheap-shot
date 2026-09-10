package frames

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestRotateJPEG(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 40, 20))
	colors := [2][2]color.RGBA{
		{{255, 0, 0, 255}, {0, 255, 0, 255}},
		{{0, 0, 255, 255}, {255, 255, 0, 255}},
	}
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.SetRGBA(x, y, colors[y/10][x/20])
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		degrees       int
		width, height int
		points        [4]struct {
			x, y int
			c    color.RGBA
		}
	}{
		{0, 40, 20, [4]struct {
			x, y int
			c    color.RGBA
		}{{10, 5, colors[0][0]}, {30, 5, colors[0][1]}, {10, 15, colors[1][0]}, {30, 15, colors[1][1]}}},
		{90, 20, 40, [4]struct {
			x, y int
			c    color.RGBA
		}{{14, 10, colors[0][0]}, {14, 30, colors[0][1]}, {4, 10, colors[1][0]}, {4, 30, colors[1][1]}}},
		{180, 40, 20, [4]struct {
			x, y int
			c    color.RGBA
		}{{29, 14, colors[0][0]}, {9, 14, colors[0][1]}, {29, 4, colors[1][0]}, {9, 4, colors[1][1]}}},
		{270, 20, 40, [4]struct {
			x, y int
			c    color.RGBA
		}{{5, 29, colors[0][0]}, {5, 9, colors[0][1]}, {15, 29, colors[1][0]}, {15, 9, colors[1][1]}}},
	}
	for _, tc := range cases {
		t.Run(string(rune('0'+tc.degrees/90)), func(t *testing.T) {
			got, err := RotateJPEG(encoded.Bytes(), tc.degrees)
			if err != nil {
				t.Fatal(err)
			}
			if tc.degrees == 0 && !bytes.Equal(got, encoded.Bytes()) {
				t.Fatal("zero rotation re-encoded the frame")
			}
			decoded, err := jpeg.Decode(bytes.NewReader(got))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != tc.width || decoded.Bounds().Dy() != tc.height {
				t.Fatalf("dimensions = %dx%d, want %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy(), tc.width, tc.height)
			}
			for _, point := range tc.points {
				gotColor := color.RGBAModel.Convert(decoded.At(point.x, point.y)).(color.RGBA)
				if colorDistance(gotColor, point.c) > 60 {
					t.Fatalf("pixel (%d,%d) = %#v, want near %#v", point.x, point.y, gotColor, point.c)
				}
			}
		})
	}
}

func colorDistance(got, want color.RGBA) int {
	red := int(got.R) - int(want.R)
	green := int(got.G) - int(want.G)
	blue := int(got.B) - int(want.B)
	if red < 0 {
		red = -red
	}
	if green < 0 {
		green = -green
	}
	if blue < 0 {
		blue = -blue
	}
	return red + green + blue
}
