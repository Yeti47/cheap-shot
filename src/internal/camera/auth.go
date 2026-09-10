package camera

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// Deckey constants, hardcoded in the stock firmware's iot_cfg_deckey
// (logical 0x374b8): the salt appended to the did before hashing into the AES
// key, and the fixed CBC IV.
const (
	deckeySalt = "HL4viXBiGEz8mCBkuhkTQFaK"
	deckeyIV   = "e7uJ6Q8uM7ikpUxf"
)

// Deckey recovers a device secret from its stored, encrypted config form,
// exactly as the firmware's iot_cfg_deckey does at config-load time. The auth
// "scode" is Deckey(did, lslat) -- NOT the plaintext product_secret. Recovered
// from iot_dev_cfg_loadmem (0x379c4) / iot_cfg_deckey (0x374b8):
//
//	ciphertext = base64(value)
//	key        = MD5_hex(did + "HL4viXBiGEz8mCBkuhkTQFaK")   // 32 hex bytes = AES-256 key
//	iv         = "e7uJ6Q8uM7ikpUxf"
//	plaintext  = AES-256-CBC-decrypt(ciphertext, key, iv), PKCS#7 stripped
func Deckey(did, value string) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("camera: deckey base64: %w", err)
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return "", errors.New("camera: deckey ciphertext not block-aligned")
	}
	sum := md5.Sum([]byte(did + deckeySalt))
	key := []byte(hex.EncodeToString(sum[:])) // 32 ASCII hex bytes -> AES-256
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, []byte(deckeyIV)).CryptBlocks(pt, ct)
	pt, err = pkcs7Strip(pt)
	if err != nil {
		return "", fmt.Errorf("camera: deckey unpad: %w", err)
	}
	return string(pt), nil
}

func pkcs7Strip(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, errors.New("bad padded length")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("bad PKCS#7 padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("bad PKCS#7 padding")
		}
	}
	return b[:len(b)-n], nil
}

// LanPassword derives the LanAuth password the firmware expects.
//
//	password = "$L<idx>$" + MD5_hex("<did>-<scode>-<idx>")
//
// Recovered from the auth2 builder (logical 0x2bf50). Note that `scode` here is
// the auth secret Deckey(did, lslat) -- the AES-decrypted config field, NOT the
// factory record's PRODUCT_SECRET. Both did and lslat are device-local values,
// so this needs neither the vendor app nor a cloud account.
func LanPassword(did, scode string, idx int) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s-%s-%d", did, scode, idx)))
	return fmt.Sprintf("$L%d$%s", idx, hex.EncodeToString(sum[:]))
}
