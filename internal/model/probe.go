package model

import (
	"crypto/hmac"
	"crypto/sha256"
)

const LocalProbePath = "/api/v1/probe"

// LegacyLocalProbePath is retained for v1.x.x client compatibility and will be
// deprecated in v2.x.x. Keep its request and proof semantics unchanged in v1.
const LegacyLocalProbePath = "/probe"

const DNSQueryPath = "/api/v1/dns-query"

// VersionedProbeProof binds the product version to the nonce-bound identity proof.
func VersionedProbeProof(identityProof []byte, serverVersion string) []byte {
	mac := hmac.New(sha256.New, identityProof)
	_, _ = mac.Write([]byte("fncpn-product-version\x00"))
	_, _ = mac.Write([]byte(serverVersion))
	return mac.Sum(nil)
}
