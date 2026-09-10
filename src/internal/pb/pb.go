// Package pb implements the sliver of protobuf wire format the camera needs.
//
// The firmware's RPC payloads are a handful of varint and length-delimited
// fields and no .proto schema exists for them, so a full protobuf runtime
// would buy nothing here.
package pb

import (
	"errors"
	"fmt"
)

// Wire types we can encounter while scanning a payload.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

var (
	// ErrTruncated means the payload ended in the middle of a field.
	ErrTruncated = errors.New("pb: truncated payload")
	// ErrFieldMissing means the requested field number was not present.
	ErrFieldMissing = errors.New("pb: field missing")
)

// AppendVarint appends v in protobuf varint encoding.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Varint encodes v on its own.
func Varint(v uint64) []byte { return AppendVarint(nil, v) }

// ReadVarint decodes one varint starting at off, returning the value and the
// offset just past it.
func ReadVarint(b []byte, off int) (uint64, int, error) {
	var v uint64
	for shift := 0; shift < 70; shift += 7 {
		if off >= len(b) {
			return 0, 0, ErrTruncated
		}
		c := b[off]
		off++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, off, nil
		}
	}
	return 0, 0, errors.New("pb: varint too long")
}

// VarintField encodes one varint field. Negative values use the two's
// complement int64 bit pattern (not zigzag) -- the camera's time-sync reply
// carries -30 that way.
func VarintField(num int, v int64) []byte {
	b := AppendVarint(nil, uint64(num)<<3|wireVarint)
	return AppendVarint(b, uint64(v))
}

// BytesField encodes one length-delimited field.
func BytesField(num int, v []byte) []byte {
	b := AppendVarint(nil, uint64(num)<<3|wireBytes)
	b = AppendVarint(b, uint64(len(v)))
	return append(b, v...)
}

// StringField encodes s as a length-delimited field.
func StringField(num int, s string) []byte { return BytesField(num, []byte(s)) }

// FirstVarint returns the first varint field with the given number, skipping
// over any other field regardless of its wire type.
func FirstVarint(payload []byte, num int) (uint64, error) {
	var found uint64
	var ok bool
	err := walk(payload, func(n, wire int, v uint64, raw []byte) bool {
		if n == num && wire == wireVarint {
			found, ok = v, true
			return false
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrFieldMissing, num)
	}
	return found, nil
}

// FirstBytes returns the first length-delimited field with the given number.
func FirstBytes(payload []byte, num int) ([]byte, error) {
	var found []byte
	var ok bool
	err := walk(payload, func(n, wire int, v uint64, raw []byte) bool {
		if n == num && wire == wireBytes {
			found, ok = raw, true
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrFieldMissing, num)
	}
	return found, nil
}

// walk visits every field in payload. The callback returns false to stop.
func walk(payload []byte, fn func(num, wire int, v uint64, raw []byte) bool) error {
	off := 0
	for off < len(payload) {
		tag, next, err := ReadVarint(payload, off)
		if err != nil {
			return err
		}
		off = next
		num, wire := int(tag>>3), int(tag&7)
		if num == 0 {
			return errors.New("pb: field number 0")
		}
		switch wire {
		case wireVarint:
			v, next, err := ReadVarint(payload, off)
			if err != nil {
				return err
			}
			off = next
			if !fn(num, wire, v, nil) {
				return nil
			}
		case wireBytes:
			n, next, err := ReadVarint(payload, off)
			if err != nil {
				return err
			}
			end := next + int(n)
			if int(n) < 0 || end > len(payload) || end < next {
				return ErrTruncated
			}
			off = end
			if !fn(num, wire, 0, payload[next:end]) {
				return nil
			}
		case wireFixed64, wireFixed32:
			width := 8
			if wire == wireFixed32 {
				width = 4
			}
			if off+width > len(payload) {
				return ErrTruncated
			}
			raw := payload[off : off+width]
			off += width
			if !fn(num, wire, 0, raw) {
				return nil
			}
		default:
			return fmt.Errorf("pb: unsupported wire type %d", wire)
		}
	}
	return nil
}
