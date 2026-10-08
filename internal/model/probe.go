package model

import (
	"crypto/hmac"
	"crypto/sha256"
)

// VersionedProbeProof binds the product version to the nonce-bound identity proof.
func VersionedProbeProof(identityProof []byte, serverVersion string) []byte {
	mac := hmac.New(sha256.New, identityProof)
	_, _ = mac.Write([]byte("fncpn-product-version\x00"))
	_, _ = mac.Write([]byte(serverVersion))
	return mac.Sum(nil)
}
