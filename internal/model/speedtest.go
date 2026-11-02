package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const SpeedTestPath = "/api/v1/speedtest"
const SpeedTestDurationMS = 8000
const SpeedTestMaxBytes int64 = 512 << 20

// The proof authorizes one HTTP request, not a server-side test session.
func SpeedTestProof(key []byte, device, method, path string, timestamp int64) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "fncpn-speedtest-v2\n%s\n%s\n%s\n%d", device, method, path, timestamp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type SpeedTestStats struct {
	Bytes      int64 `json:"receivedBytes"`
	ElapsedMS  int64 `json:"elapsedMs"`
	TotalBytes int64 `json:"totalBytes"`
}
