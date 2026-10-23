package support

import (
	"strings"
	"testing"
)

func TestReportDropsIdentitySecretsAndFreeFormErrors(t *testing.T) {
	raw := `{"direct":{"reason":"no_local_ipv6","localPublicIPv6":false,"endpoint":"sensitive-host"},"FNID":"secret-id","status":{"state":"RELAY"},"errors":[{"code":"NETWORK_ERROR","message":"password=secret-value"}]}`
	data, err := Report([]byte(raw), map[string]string{"client.log": `{"msg":"client connected","state":"RELAY","device_id":"secret-device","password":"secret-password"}` + "\n" + `{"msg":"secret-error-text","error":"secret-error"}`, "credentials": "secret-file"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"secret", "sensitive", "endpoint", "password", "device_id"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("export leaked %s: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "no_local_ipv6") || !strings.Contains(text, "client connected") {
		t.Fatal("lost useful diagnostic events")
	}
}
