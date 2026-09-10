package pprpc

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
)

func TestParseHeader(t *testing.T) {
	tcp := false
	h, off, err := ParseHeader([]byte{0x48, 0x05, 1, 2, 3, 4, 5}, &tcp)
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != TypeRPC || h.Flag != FlagNormal || h.Length != 5 || off != 2 {
		t.Fatalf("got %+v, off %d", h, off)
	}
}

func TestParseHeaderUDPMagic(t *testing.T) {
	h, off, err := ParseHeader(append(append([]byte{}, UDPMagic...), 0x48, 0x00), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !h.UDP || off != 4 {
		t.Fatalf("got %+v, off %d", h, off)
	}
}

func TestParseHeaderRejects(t *testing.T) {
	tcp := false
	if _, _, err := ParseHeader([]byte{0x18, 0x00}, &tcp); err == nil {
		t.Fatal("want an error for message type 1")
	}
	if _, _, err := ParseHeader([]byte{0x40, 0x00}, &tcp); err == nil {
		t.Fatal("want an error for flag 0")
	}
	if _, _, err := ParseHeader([]byte{}, &tcp); !errors.Is(err, ErrShort) {
		t.Fatalf("want ErrShort, got %v", err)
	}
}

func TestPackAndParseRPCRoundTrip(t *testing.T) {
	plain := pb.StringField(3, "$L0$deadbeef")
	frame, err := PackRPC(1, 2650, RPCRequest, 0, plain, DefaultPrekey)
	if err != nil {
		t.Fatal(err)
	}
	tcp := false
	p, err := Parse(frame, &tcp)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := p.(*RPC)
	if !ok {
		t.Fatalf("got %T, want *RPC", p)
	}
	if r.Sequence != 1 || r.CommandID != 2650 || r.RPCType != RPCRequest || r.Encryption != EncAES256CBC {
		t.Fatalf("unexpected header: %+v", r)
	}
	key, iv := RPCKey(DefaultPrekey, r.Sequence, r.CommandID, uint64(r.RPCType))
	got, err := DecryptCBCPadded(r.Payload, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("payload round-trip mismatch")
	}
}

func TestPackRPCEmptyPayloadSendsNoPaddingBlock(t *testing.T) {
	frame, err := PackRPC(4, 2614, RPCRequest, 0, nil, DefaultPrekey)
	if err != nil {
		t.Fatal(err)
	}
	tcp := false
	p, _ := Parse(frame, &tcp)
	if got := len(p.(*RPC).Payload); got != 0 {
		t.Fatalf("empty request carried %d payload bytes", got)
	}
}

func TestParseResponseCarriesCode(t *testing.T) {
	frame, err := PackRPC(2, 106, RPCResponse, 7, nil, DefaultPrekey)
	if err != nil {
		t.Fatal(err)
	}
	tcp := false
	p, err := Parse(frame, &tcp)
	if err != nil {
		t.Fatal(err)
	}
	r := p.(*RPC)
	if !r.HasCode || r.ResponseCode != 7 {
		t.Fatalf("got code %d (present %v)", r.ResponseCode, r.HasCode)
	}
}

func TestParseAV(t *testing.T) {
	body := []byte{0x80 | FormatMJPEG, 3}
	body = append(body, pb.Varint(0)...)    // channel
	body = append(body, pb.Varint(42)...)   // sequence
	body = append(body, pb.Varint(9999)...) // timestamp
	body = append(body, pb.Varint(1040)...) // encrypted length
	body = append(body, 1, 1, 0, 0xaa)      // fragment header + one byte
	frame := append([]byte{byte(TypeAV<<4 | FlagVideoFrag)}, pb.Varint(uint64(len(body)))...)
	frame = append(frame, body...)

	tcp := false
	p, err := Parse(frame, &tcp)
	if err != nil {
		t.Fatal(err)
	}
	av, ok := p.(*AV)
	if !ok {
		t.Fatalf("got %T, want *AV", p)
	}
	if !av.KeyFrame || av.MediaFormat != FormatMJPEG || av.Sequence != 42 ||
		av.Timestamp != 9999 || av.EncryptedLength != 1040 {
		t.Fatalf("unexpected AV header: %+v", av)
	}
	if !bytes.Equal(av.Payload, []byte{1, 1, 0, 0xaa}) {
		t.Fatalf("payload = %x", av.Payload)
	}
}

func TestSplitTCPKeepsPartialTail(t *testing.T) {
	a, _ := PackRPC(1, 106, RPCRequest, 0, pb.VarintField(1, 1), DefaultPrekey)
	b, _ := PackRPC(2, 107, RPCRequest, 0, pb.VarintField(1, 2), DefaultPrekey)
	stream := append(append([]byte{}, a...), b[:len(b)-3]...)

	packets, rest, err := SplitTCP(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1", len(packets))
	}
	if !bytes.Equal(rest, b[:len(b)-3]) {
		t.Fatalf("remainder mismatch")
	}
	// Feeding the rest of the second packet completes it.
	packets, rest, err = SplitTCP(append(rest, b[len(b)-3:]...))
	if err != nil || len(packets) != 1 || len(rest) != 0 {
		t.Fatalf("second pass: %d packets, %d rest, %v", len(packets), len(rest), err)
	}
}

// Derivation vectors computed independently from the format strings recovered
// in the firmware (docs/protocol.md).
func TestRPCKeyVector(t *testing.T) {
	key, iv := RPCKey(DefaultPrekey, 1, 2650, RPCRequest)
	const want = "9ff1ba1a2fb7e397eec195e7083bb8fc"
	if string(key) != want {
		t.Fatalf("key = %s, want %s", key, want)
	}
	if string(iv) != want[:16] {
		t.Fatalf("RPC IV must be the FIRST 16 bytes of the digest, got %s", iv)
	}
}

func TestAVKeyVector(t *testing.T) {
	session := []byte("0123456789abcdef0123456789abcdef")
	key, iv := AVKey(session, 5, 1234, 0)
	const want = "8b7af803fea1cd5451ec6920335cf109"
	if string(key) != want {
		t.Fatalf("key = %s, want %s", key, want)
	}
	if string(iv) != want[16:] {
		t.Fatalf("A/V IV must be the LAST 16 bytes of the digest, got %s", iv)
	}
}

func TestCBCRoundTrips(t *testing.T) {
	key, iv := RPCKey(DefaultPrekey, 1, 2610, RPCRequest)
	padded, err := EncryptCBCPadded([]byte("a short body"), key, iv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptCBCPadded(padded, key, iv)
	if err != nil || string(got) != "a short body" {
		t.Fatalf("padded round trip: %q %v", got, err)
	}

	block := bytes.Repeat([]byte{0x5a}, 32)
	enc, err := EncryptCBCUnpadded(block, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecryptCBCUnpadded(enc, key, iv)
	if err != nil || !bytes.Equal(dec, block) {
		t.Fatalf("unpadded round trip: %v", err)
	}
	if _, err := DecryptCBCUnpadded(enc[:31], key, iv); err == nil {
		t.Fatal("want an error for a non-block-aligned ciphertext")
	}
}
