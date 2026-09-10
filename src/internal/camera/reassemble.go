package camera

import (
	"bytes"

	"github.com/Yeti47/cheap-shot/src/internal/pprpc"
)

// Fragment header: 0x01, index, 0x00. Indices start at 1; 0xff marks the last
// fragment of a frame.
const (
	fragMarker   = 0x01
	fragLast     = 0xff
	fragHeader   = 3
	maxFrameSize = 2 << 20
)

var (
	jpegSOI = []byte{0xff, 0xd8}
	jpegEOI = []byte{0xff, 0xd9}
)

type frameBuf struct {
	parts   [][]byte
	size    int
	meta    *pprpc.AV // the fragment carrying timestamp/encrypted length
	nextIdx int
}

// Reassembler turns the camera's fragmented, partially-encrypted type-6 MJPEG
// packets back into whole JPEG images.
type Reassembler struct {
	sessionKey []byte
	frames     map[uint64]*frameBuf
	dropped    int
}

// NewReassembler returns a reassembler bound to one LanAuth session key.
func NewReassembler(sessionKey []byte) *Reassembler {
	return &Reassembler{sessionKey: sessionKey, frames: map[uint64]*frameBuf{}}
}

// Dropped reports how many frames were discarded as malformed or incomplete.
func (r *Reassembler) Dropped() int { return r.dropped }

// Push feeds one A/V packet in. It returns a complete JPEG image, or nil while
// a frame is still being assembled or was discarded.
func (r *Reassembler) Push(p *pprpc.AV) []byte {
	if p.MediaFormat != pprpc.FormatMJPEG || len(p.Payload) < fragHeader {
		return nil
	}
	if p.Payload[0] != fragMarker || p.Payload[2] != 0 {
		r.drop(p.Sequence)
		return nil
	}
	idx := int(p.Payload[1])

	fb := r.frames[p.Sequence]
	if idx == 1 {
		// A restarted first fragment supersedes anything held for this frame.
		r.drop(p.Sequence)
		fb = &frameBuf{nextIdx: 1}
		r.frames[p.Sequence] = fb
	}
	if fb == nil {
		// A lone final fragment is either a whole small frame or the tail of
		// one we joined mid-flight; the latter has no metadata and is dropped
		// by finish.
		if idx != fragLast {
			return nil
		}
		fb = &frameBuf{nextIdx: 1}
		r.frames[p.Sequence] = fb
	}
	if idx != fragLast && idx != fb.nextIdx {
		r.drop(p.Sequence)
		return nil
	}

	part := p.Payload[fragHeader:]
	fb.parts = append(fb.parts, part)
	fb.size += len(part)
	if fb.size > maxFrameSize {
		r.drop(p.Sequence)
		return nil
	}
	if p.EncryptedLength > 0 && fb.meta == nil {
		fb.meta = p
	}
	if idx != fragLast {
		fb.nextIdx++
		return nil
	}

	delete(r.frames, p.Sequence)
	frame, ok := r.finish(fb)
	if !ok {
		r.dropped++
		return nil
	}
	return frame
}

func (r *Reassembler) finish(fb *frameBuf) ([]byte, bool) {
	if fb.meta == nil {
		return nil, false
	}
	assembled := bytes.Join(fb.parts, nil)
	n := int(fb.meta.EncryptedLength)
	if n <= 0 || n > len(assembled) || n%16 != 0 {
		return nil, false
	}
	key, iv := pprpc.AVKey(r.sessionKey, fb.meta.Sequence, fb.meta.Timestamp, fb.meta.Channel)
	head, err := pprpc.DecryptCBCUnpadded(assembled[:n], key, iv)
	if err != nil {
		return nil, false
	}
	frame := append(head, assembled[n:]...)
	if !bytes.HasPrefix(frame, jpegSOI) {
		return nil, false
	}
	// The firmware appends five bytes after EOI; cutting at EOI drops them.
	end := bytes.Index(frame[2:], jpegEOI)
	if end < 0 {
		return nil, false
	}
	return frame[:end+2+len(jpegEOI)], true
}

func (r *Reassembler) drop(seq uint64) {
	if _, ok := r.frames[seq]; ok {
		r.dropped++
		delete(r.frames, seq)
	}
}
