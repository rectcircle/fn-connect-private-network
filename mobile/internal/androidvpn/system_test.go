package androidvpn

import "testing"

func TestBridgeRejectsMismatchedVersionAndAcceptsUnknownFields(t *testing.T) {
	for _, raw := range []string{`{"ok":true,"result":7}`, `{"version":2,"ok":true,"result":7}`} {
		var fd int
		if err := DecodeReply([]byte(raw), &fd); err == nil {
			t.Fatal("bridge version mismatch was accepted")
		}
	}
	var fd int
	if err := DecodeReply([]byte(`{"version":1,"ok":true,"result":7,"future":"ignored"}`), &fd); err != nil || fd != 7 {
		t.Fatalf("compatible response: %v", err)
	}
	if err := DecodeReply([]byte(`{"version":1,"ok":false,"error":"VPN revoked"}`), nil); err == nil || err.Error() != "VPN revoked" {
		t.Fatal("failure lost at JNI boundary")
	}
}
