package pb

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 300, 16383, 16384, 1 << 32, ^uint64(0)} {
		enc := Varint(v)
		got, off, err := ReadVarint(enc, 0)
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", v, err)
		}
		if got != v || off != len(enc) {
			t.Fatalf("Varint(%d) round-tripped to %d after %d/%d bytes", v, got, off, len(enc))
		}
	}
}

func TestReadVarintTruncated(t *testing.T) {
	if _, _, err := ReadVarint([]byte{0x80}, 0); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestFieldRoundTrip(t *testing.T) {
	payload := VarintField(1, 12345)
	payload = append(payload, StringField(3, "hello")...)
	payload = append(payload, VarintField(2, -30)...)

	if got, err := FirstVarint(payload, 1); err != nil || got != 12345 {
		t.Fatalf("field 1 = %d, %v", got, err)
	}
	if got, err := FirstBytes(payload, 3); err != nil || string(got) != "hello" {
		t.Fatalf("field 3 = %q, %v", got, err)
	}
	// Negative values travel as the two's complement int64 pattern.
	got, err := FirstVarint(payload, 2)
	if err != nil || int64(got) != -30 {
		t.Fatalf("field 2 = %d (%v)", int64(got), err)
	}
}

func TestFieldSkipsOtherWireTypes(t *testing.T) {
	// A fixed32 field (wire type 5) in front must not derail the scan.
	payload := append(Varint(1<<3|5), 1, 2, 3, 4)
	payload = append(payload, StringField(2, "target")...)
	got, err := FirstBytes(payload, 2)
	if err != nil || !bytes.Equal(got, []byte("target")) {
		t.Fatalf("field 2 = %q, %v", got, err)
	}
}

func TestFieldMissing(t *testing.T) {
	if _, err := FirstVarint(VarintField(1, 5), 9); !errors.Is(err, ErrFieldMissing) {
		t.Fatalf("want ErrFieldMissing, got %v", err)
	}
}
