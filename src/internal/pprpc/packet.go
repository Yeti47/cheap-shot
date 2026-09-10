// Package pprpc implements the XC Things pprpc/avsdk framing spoken by the
// camera on TCP and UDP port 20190.
//
// The wire format is documented in docs/protocol.md; it was recovered by
// static analysis of the stock firmware and cross-checked against the public
// ino-a9-local-bridge client.
package pprpc

import (
	"errors"
	"fmt"

	"github.com/Yeti47/cheap-shot/src/internal/pb"
)

// Port is the only TCP/UDP service the firmware ever exposes.
const Port = 20190

// Message types carried in the high nibble of the first header byte.
const (
	TypeHeartbeat = 3
	TypeRPC       = 4 // protobuf RPC
	TypeJSONRPC   = 5
	TypeAV        = 6
	TypeCustom    = 7
	TypeFile      = 8
)

// Flags carried in the low nibble of the first header byte.
const (
	FlagNormal    = 8
	FlagAudio     = 9
	FlagVideoFrag = 10
)

// RPC directions and payload encryption types.
const (
	RPCRequest  = 0
	RPCResponse = 1

	EncNone      = 0
	EncAES256CBC = 3
)

// Media format values seen in A/V packets.
const (
	FormatMJPEG    = 4
	FormatG711ALaw = 21
)

// UDPMagic prefixes every pprpc packet sent over UDP.
var UDPMagic = []byte{'Q', 'p'}

// ErrShort reports that the buffer holds less than one complete packet.
var ErrShort = errors.New("pprpc: short buffer")

// Header is the fixed header every packet starts with.
type Header struct {
	Type   int
	Flag   int
	Length int  // declared body length
	UDP    bool // the "Qp" prefix was present
	Size   int  // header size in bytes
}

// Packet is an RPC, A/V or otherwise-unparsed pprpc packet.
type Packet interface{ Head() Header }

// RPC is a type-4/5 control packet. Payload is still encrypted.
type RPC struct {
	Header       Header
	Sequence     uint64
	CommandID    uint64
	Encryption   int
	RPCType      int
	ResponseCode uint64
	HasCode      bool
	Payload      []byte
}

// AV is a type-6 audio/video packet.
type AV struct {
	Header          Header
	KeyFrame        bool
	MediaFormat     int
	Encryption      int
	Channel         uint64
	Sequence        uint64
	Timestamp       uint64
	EncryptedLength uint64
	Payload         []byte
}

// Raw is any packet type we do not decode further.
type Raw struct {
	Header  Header
	Payload []byte
}

func (p *RPC) Head() Header { return p.Header }
func (p *AV) Head() Header  { return p.Header }
func (p *Raw) Head() Header { return p.Header }

// ParseHeader decodes a fixed header. When udp is nil the "Qp" prefix is
// auto-detected.
func ParseHeader(b []byte, udp *bool) (Header, int, error) {
	off := 0
	detected := len(b) >= 2 && b[0] == UDPMagic[0] && b[1] == UDPMagic[1]
	isUDP := detected
	if udp != nil {
		isUDP = *udp
		if isUDP && !detected {
			return Header{}, 0, fmt.Errorf("pprpc: missing UDP magic")
		}
	}
	if isUDP {
		off = len(UDPMagic)
	}
	if off >= len(b) {
		return Header{}, 0, ErrShort
	}
	tf := b[off]
	off++
	typ, flag := int(tf>>4), int(tf&0x0f)
	if typ < TypeHeartbeat || typ > TypeFile {
		return Header{}, 0, fmt.Errorf("pprpc: unsupported message type %d", typ)
	}
	if flag < FlagNormal || flag > FlagVideoFrag {
		return Header{}, 0, fmt.Errorf("pprpc: unsupported flag %d", flag)
	}
	length, next, err := pb.ReadVarint(b, off)
	if err != nil {
		return Header{}, 0, ErrShort
	}
	return Header{Type: typ, Flag: flag, Length: int(length), UDP: isUDP, Size: next}, next, nil
}

// Parse decodes exactly one packet from b, which must contain the whole packet.
func Parse(b []byte, udp *bool) (Packet, error) {
	h, off, err := ParseHeader(b, udp)
	if err != nil {
		return nil, err
	}
	end := off + h.Length
	if end > len(b) {
		return nil, ErrShort
	}
	body := b[off:end]
	switch h.Type {
	case TypeRPC, TypeJSONRPC:
		return parseRPC(h, body)
	case TypeAV:
		return parseAV(h, body)
	default:
		return &Raw{Header: h, Payload: body}, nil
	}
}

// SplitTCP decodes every complete packet in buf. A trailing partial packet is
// returned as the remainder instead of being an error.
func SplitTCP(buf []byte) ([]Packet, []byte, error) {
	tcp := false
	var out []Packet
	off := 0
	for off < len(buf) {
		h, hdrSize, err := ParseHeader(buf[off:], &tcp)
		if errors.Is(err, ErrShort) {
			break
		}
		if err != nil {
			return out, buf[off:], err
		}
		size := hdrSize + h.Length
		if len(buf)-off < size {
			break
		}
		p, err := Parse(buf[off:off+size], &tcp)
		if err != nil {
			return out, buf[off:], err
		}
		out = append(out, p)
		off += size
	}
	return out, buf[off:], nil
}

func parseRPC(h Header, body []byte) (*RPC, error) {
	off := 0
	seq, off, err := pb.ReadVarint(body, off)
	if err != nil {
		return nil, err
	}
	cmd, off, err := pb.ReadVarint(body, off)
	if err != nil {
		return nil, err
	}
	if off >= len(body) {
		return nil, fmt.Errorf("pprpc: RPC packet missing encryption byte")
	}
	flags := body[off]
	off++
	p := &RPC{
		Header:     h,
		Sequence:   seq,
		CommandID:  cmd,
		Encryption: int(flags >> 2),
		RPCType:    int(flags & 0x03),
	}
	if p.RPCType != RPCRequest && p.RPCType != RPCResponse {
		return nil, fmt.Errorf("pprpc: unsupported RPC type %d", p.RPCType)
	}
	if p.RPCType == RPCResponse {
		code, next, err := pb.ReadVarint(body, off)
		if err != nil {
			return nil, err
		}
		p.ResponseCode, p.HasCode, off = code, true, next
	}
	p.Payload = body[off:]
	return p, nil
}

func parseAV(h Header, body []byte) (*AV, error) {
	if len(body) < 2 {
		return nil, fmt.Errorf("pprpc: AV packet missing format bytes")
	}
	p := &AV{
		Header:      h,
		KeyFrame:    body[0]>>7 == 1,
		MediaFormat: int(body[0] & 0x7f),
		Encryption:  int(body[1]),
	}
	off := 2
	var err error
	if p.Channel, off, err = pb.ReadVarint(body, off); err != nil {
		return nil, err
	}
	if p.Sequence, off, err = pb.ReadVarint(body, off); err != nil {
		return nil, err
	}
	if p.Timestamp, off, err = pb.ReadVarint(body, off); err != nil {
		return nil, err
	}
	if p.EncryptedLength, off, err = pb.ReadVarint(body, off); err != nil {
		return nil, err
	}
	p.Payload = body[off:]
	return p, nil
}

// PackRPC frames one RPC packet. plaintext is encrypted with the given prekey
// unless it is empty, in which case no payload (and no padding block) is sent.
func PackRPC(seq, cmd uint64, rpcType int, responseCode uint64, plaintext []byte, prekey string) ([]byte, error) {
	var payload []byte
	if len(plaintext) > 0 {
		key, iv := RPCKey(prekey, seq, cmd, uint64(rpcType))
		var err error
		if payload, err = EncryptCBCPadded(plaintext, key, iv); err != nil {
			return nil, err
		}
	}
	body := pb.AppendVarint(nil, seq)
	body = pb.AppendVarint(body, cmd)
	body = append(body, byte(EncAES256CBC<<2|rpcType))
	if rpcType == RPCResponse {
		body = pb.AppendVarint(body, responseCode)
	}
	body = append(body, payload...)

	out := []byte{byte(TypeRPC<<4 | FlagNormal)}
	out = pb.AppendVarint(out, uint64(len(body)))
	return append(out, body...), nil
}

// PackUDP frames a packet for the UDP transport by prepending the "Qp" magic.
func PackUDP(tcpFramed []byte) []byte {
	return append(append([]byte{}, UDPMagic...), tcpFramed...)
}
