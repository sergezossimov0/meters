// Package sign computes and checks the HashSHA256 body signature shared by
// the server and the gateways (spec п. 6.1): hex(HMAC-SHA256(body, KEY)),
// always over the uncompressed body.
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Header carries the signature. Go canonicalizes it to "Hashsha256" on the
// wire; header names are case-insensitive (RFC 9110 §5.1), so any client
// that looks it up properly finds it.
const Header = "HashSHA256"

// Sum returns the hex HMAC-SHA256 of body under key.
func Sum(body, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig is the signature of body under key. The
// comparison takes constant time; a malformed sig is simply not valid.
func Verify(body, key []byte, sig string) bool {
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
