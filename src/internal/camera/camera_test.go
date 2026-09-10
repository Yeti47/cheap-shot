package camera

import (
	"bytes"
	"testing"

	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

func TestLanPassword(t *testing.T) {
	// password = "$L<idx>$" + MD5("<did>-<scode>-<idx>"), from local_check_auth1.
	got := LanPassword("PP00A90AC5645E", "12345", 0)
	const want = "$L0$1feef0bb87b6a9cc8e5c1d1090d74613"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if got := LanPassword("PP00A90AC5645E", "12345", 3); got != "$L3$f8e3a352513d1e2c0ce47d46d30b7bfb" {
		t.Fatalf("index 3 derivation wrong: %s", got)
	}
}

var testSession = []byte("0123456789abcdef0123456789abcdef")

// buildFrame produces the packets the camera would emit for one JPEG: the
// head AES-encrypted without padding, the rest plaintext, five trailer bytes
// after EOI, split across fragments.
func buildFrame(t *testing.T, seq uint64, payloadLen, encLen, fragments int) ([]*pprpc.AV, []byte) {
	t.Helper()
	jpeg := make([]byte, payloadLen)
	jpeg[0], jpeg[1] = 0xff, 0xd8
	for i := 2; i < payloadLen-7; i++ {
		jpeg[i] = byte(i)
	}
	copy(jpeg[payloadLen-7:], []byte{0xff, 0xd9, 1, 2, 3, 4, 5}) // EOI + trailer

	const ts, ch = 4242, 0
	key, iv := pprpc.AVKey(testSession, seq, ts, ch)
	head, err := pprpc.EncryptCBCUnpadded(jpeg[:encLen], key, iv)
	if err != nil {
		t.Fatal(err)
	}
	wire := append(append([]byte{}, head...), jpeg[encLen:]...)

	var packets []*pprpc.AV
	chunk := (len(wire) + fragments - 1) / fragments
	for i := range fragments {
		start := i * chunk
		end := min(start+chunk, len(wire))
		idx := byte(i + 1)
		if i == fragments-1 {
			idx = 0xff
		}
		p := &pprpc.AV{
			MediaFormat: pprpc.FormatMJPEG,
			Channel:     ch,
			Sequence:    seq,
			Timestamp:   ts,
			Payload:     append([]byte{1, idx, 0}, wire[start:end]...),
		}
		if i == 0 {
			p.EncryptedLength = uint64(encLen)
		}
		packets = append(packets, p)
	}
	return packets, jpeg[:payloadLen-5] // expected: everything through EOI
}

func TestReassemblerHappyPath(t *testing.T) {
	packets, want := buildFrame(t, 7, 2048, 1040, 3)
	re := NewReassembler(testSession)
	var got []byte
	for _, p := range packets {
		if frame := re.Push(p); frame != nil {
			got = frame
		}
	}
	if got == nil {
		t.Fatal("no frame assembled")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("frame mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if !bytes.HasSuffix(got, []byte{0xff, 0xd9}) {
		t.Fatal("frame must end at EOI, with the 5 trailer bytes removed")
	}
}

func TestReassemblerSingleFragment(t *testing.T) {
	packets, want := buildFrame(t, 9, 1200, 1040, 1)
	re := NewReassembler(testSession)
	got := re.Push(packets[0])
	if !bytes.Equal(got, want) {
		t.Fatalf("single-fragment frame mismatch (%d vs %d bytes)", len(got), len(want))
	}
}

func TestReassemblerDropsGap(t *testing.T) {
	packets, _ := buildFrame(t, 12, 4096, 1040, 4)
	re := NewReassembler(testSession)
	re.Push(packets[0])
	if frame := re.Push(packets[2]); frame != nil { // fragment 3 where 2 was due
		t.Fatal("a gap must not produce a frame")
	}
	if re.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", re.Dropped())
	}
}

func TestReassemblerRejectsUnknownSequence(t *testing.T) {
	packets, _ := buildFrame(t, 13, 2048, 1040, 3)
	re := NewReassembler(testSession)
	if frame := re.Push(packets[1]); frame != nil { // never saw fragment 1
		t.Fatal("want nil for a frame whose start we missed")
	}
	// Joining mid-frame: the tail carries no metadata, so it cannot be
	// mistaken for a complete image.
	if frame := re.Push(packets[2]); frame != nil {
		t.Fatal("want nil for a tail fragment with no frame metadata")
	}
}

func TestReassemblerRejectsBadEncryptedLength(t *testing.T) {
	packets, _ := buildFrame(t, 14, 2048, 1040, 1)
	packets[0].EncryptedLength = 1041 // not a multiple of the AES block size
	re := NewReassembler(testSession)
	if frame := re.Push(packets[0]); frame != nil {
		t.Fatal("want nil for a misaligned encrypted length")
	}
}

func TestReassemblerIgnoresNonMJPEG(t *testing.T) {
	re := NewReassembler(testSession)
	audio := &pprpc.AV{MediaFormat: pprpc.FormatG711ALaw, Payload: []byte{1, 0, 0, 9}}
	if frame := re.Push(audio); frame != nil {
		t.Fatal("audio must not be treated as video")
	}
}
