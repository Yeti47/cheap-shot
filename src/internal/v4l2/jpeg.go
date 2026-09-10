package v4l2

import "errors"

// errNoSOF means the buffer held no JPEG frame header.
var errNoSOF = errors.New("v4l2: no JPEG SOF marker")

// jpegDimensions reads the image size out of a JPEG's frame header. The
// camera clamps everything to 640x480, but reading beats assuming.
func jpegDimensions(b []byte) (width, height int, err error) {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 0, 0, errNoSOF
	}
	for i := 2; i+3 < len(b); {
		if b[i] != 0xff {
			i++
			continue
		}
		marker := b[i+1]
		switch {
		case marker == 0xff || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd9):
			i += 2 // padding, TEM or standalone marker: no length field
			continue
		}
		if i+4 > len(b) {
			return 0, 0, errNoSOF
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		// SOF0..SOF15 carry the dimensions; DHT/JPG/DAC are not frame headers.
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			if i+9 > len(b) {
				return 0, 0, errNoSOF
			}
			height = int(b[i+5])<<8 | int(b[i+6])
			width = int(b[i+7])<<8 | int(b[i+8])
			return width, height, nil
		}
		if segLen < 2 {
			return 0, 0, errNoSOF
		}
		i += 2 + segLen
	}
	return 0, 0, errNoSOF
}
