package pprpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
)

// DefaultPrekey is the control-plane key material hardcoded in the stock
// firmware (logical 0x139194, returned by the getter at 0x48e04). Every RPC
// key on the LAN path derives from it, which is what makes the camera
// drivable with no vendor app and no cloud account.
const DefaultPrekey = "A2r0i1m1a2M0a1x6toriQue"

const blockSize = aes.BlockSize

// RPCKey derives the AES-256 key and IV for one control packet. The key is the
// 32 ASCII hex characters of the MD5 digest and the IV is its FIRST 16 bytes.
func RPCKey(prekey string, seq, cmd, rpcType uint64) (key, iv []byte) {
	sum := md5.Sum([]byte(fmt.Sprintf("%s,ID:%d-SEQ:%d-RPC:%d", prekey, cmd, seq, rpcType)))
	key = []byte(hex.EncodeToString(sum[:]))
	return key, key[:blockSize]
}

// AVKey derives the AES-256 key and IV for one media frame. Note the
// asymmetry with RPCKey: here the IV is the LAST 16 bytes of the hex digest.
func AVKey(sessionKey []byte, seq, timestamp, channel uint64) (key, iv []byte) {
	sum := md5.Sum(append(append([]byte{}, sessionKey...),
		fmt.Sprintf(",AVSeq:%d-TT:%d-AVChannel:%d", seq, timestamp, channel)...))
	key = []byte(hex.EncodeToString(sum[:]))
	return key, key[len(key)-blockSize:]
}

// EncryptCBCPadded encrypts with AES-CBC and PKCS#7 padding.
func EncryptCBCPadded(plaintext, key, iv []byte) ([]byte, error) {
	block, err := newCBC(key, iv)
	if err != nil {
		return nil, err
	}
	padded := pad(plaintext)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

// DecryptCBCPadded decrypts AES-CBC and strips PKCS#7 padding.
func DecryptCBCPadded(ciphertext, key, iv []byte) ([]byte, error) {
	out, err := DecryptCBCUnpadded(ciphertext, key, iv)
	if err != nil {
		return nil, err
	}
	return unpad(out)
}

// DecryptCBCUnpadded decrypts block-aligned AES-CBC without padding, which is
// how the encrypted head of a media frame is stored.
func DecryptCBCUnpadded(ciphertext, key, iv []byte) ([]byte, error) {
	block, err := newCBC(key, iv)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%blockSize != 0 {
		return nil, errors.New("pprpc: ciphertext is not block-aligned")
	}
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	return out, nil
}

func newCBC(key, iv []byte) (cipher.Block, error) {
	if len(iv) != blockSize {
		return nil, fmt.Errorf("pprpc: IV must be %d bytes, got %d", blockSize, len(iv))
	}
	return aes.NewCipher(key)
}

func pad(b []byte) []byte {
	n := blockSize - len(b)%blockSize
	out := make([]byte, len(b), len(b)+n)
	copy(out, b)
	for range n {
		out = append(out, byte(n))
	}
	return out
}

func unpad(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%blockSize != 0 {
		return nil, errors.New("pprpc: bad padded length")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > blockSize || n > len(b) {
		return nil, errors.New("pprpc: bad PKCS#7 padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("pprpc: bad PKCS#7 padding")
		}
	}
	return b[:len(b)-n], nil
}

// EncryptCBCUnpadded encrypts block-aligned bytes with AES-CBC and no padding,
// the way a media frame's encrypted head is stored.
func EncryptCBCUnpadded(plaintext, key, iv []byte) ([]byte, error) {
	block, err := newCBC(key, iv)
	if err != nil {
		return nil, err
	}
	if len(plaintext) == 0 || len(plaintext)%blockSize != 0 {
		return nil, errors.New("pprpc: plaintext is not block-aligned")
	}
	out := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plaintext)
	return out, nil
}
