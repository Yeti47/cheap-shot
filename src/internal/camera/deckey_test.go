package camera

import "testing"

// Synthetic vectors (no real device secrets): a made-up did+secret encrypted
// with the firmware's deckey scheme, checked round-trip through Deckey. This
// pins the algorithm (base64 -> AES-256-CBC with key=MD5(did+salt), fixed IV,
// PKCS#7) without embedding any unit's credentials. The scheme itself was
// confirmed against live hardware (LanAuth returned code 0).
func TestDeckeyRoundTrip(t *testing.T) {
	const (
		did        = "EXAMPLEDID000000"
		lslat      = "54KViDQz4INdSsppQ64XBQ==" // enckey("example-secret")
		wantSecret = "example-secret"
	)
	got, err := Deckey(did, lslat)
	if err != nil {
		t.Fatalf("Deckey: %v", err)
	}
	if got != wantSecret {
		t.Fatalf("Deckey = %q, want %q", got, wantSecret)
	}
	if pw := LanPassword(did, got, 0); pw != "$L0$f08969f4a776835f3492276e43ef0777" {
		t.Fatalf("LanPassword = %s", pw)
	}
}

func TestDeckeyRejectsBadInput(t *testing.T) {
	if _, err := Deckey("did", "not valid base64!!"); err == nil {
		t.Fatal("want an error for non-base64 input")
	}
	if _, err := Deckey("did", "YWJj"); err == nil {
		t.Fatal("want an error for a non-block-aligned ciphertext")
	}
}
