package wireguard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestNormalizeClientPlan(t *testing.T) {
	plan := validClientPlan()
	normalized, err := NormalizeClientPlan(plan)
	if err != nil {
		t.Fatalf("normalize plan: %v", err)
	}
	if normalized.MTU != 1280 {
		t.Fatalf("default MTU = %d", normalized.MTU)
	}

	plan.Mode = "direct"
	plan.Endpoint = "192.0.2.1:51820"
	if _, err := NormalizeClientPlan(plan); err == nil {
		t.Fatal("IPv4 direct endpoint unexpectedly accepted")
	}
	plan.Endpoint = "[2606:4700:4700::1111]:51820"
	if _, err := NormalizeClientPlan(plan); err != nil {
		t.Fatalf("IPv6 direct endpoint rejected: %v", err)
	}
	for _, endpoint := range []string{
		"[::1]:51820",
		"[fe80::1]:51820",
		"[fd00::1]:51820",
		"[2001:db8::1]:51820",
		"[ff02::1]:51820",
	} {
		plan.Endpoint = endpoint
		if _, err := NormalizeClientPlan(plan); err == nil {
			t.Fatalf("non-public direct endpoint %q accepted", endpoint)
		}
	}

	plan = validClientPlan()
	plan.PrivateKey = testKey(0)
	if _, err := NormalizeClientPlan(plan); err == nil {
		t.Fatal("zero private key unexpectedly accepted")
	}
}

func TestNormalizeClientPlanRejectsUnsafeRoutes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.ClientPlan)
	}{
		{
			name: "public client address",
			mutate: func(plan *model.ClientPlan) {
				plan.ClientAddress = "8.8.8.8/32"
			},
		},
		{
			name: "default route",
			mutate: func(plan *model.ClientPlan) {
				plan.AllowedIPs = []string{"0.0.0.0/0"}
			},
		},
		{
			name: "public route",
			mutate: func(plan *model.ClientPlan) {
				plan.AllowedIPs = []string{"8.8.8.0/24"}
			},
		},
		{
			name: "IPv6 route",
			mutate: func(plan *model.ClientPlan) {
				plan.AllowedIPs = []string{"2001:db8::/32"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validClientPlan()
			test.mutate(&plan)
			if _, err := NormalizeClientPlan(plan); err == nil {
				t.Fatal("unsafe client plan was accepted")
			}
		})
	}
}

func TestNormalizeClientPlanAcceptsSharedAddressSpace(t *testing.T) {
	plan := validClientPlan()
	plan.ClientAddress = "100.127.203.2/32"
	plan.AllowedIPs = []string{"100.127.203.1/32", "192.168.71.0/24"}
	if _, err := NormalizeClientPlan(plan); err != nil {
		t.Fatalf("shared address plan rejected: %v", err)
	}
}

func TestNormalizeClientConfiguration(t *testing.T) {
	config := validClientConfiguration()
	normalized, err := NormalizeClientConfiguration(config)
	if err != nil {
		t.Fatalf("normalize client configuration: %v", err)
	}
	if normalized.ServerAddress != config.ServerAddress ||
		normalized.ClientAddress != config.ClientAddress {
		t.Fatalf("normalized configuration = %+v", normalized)
	}
}

func TestNormalizeClientConfigurationRejectsInvalidSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.ClientConfiguration)
	}{
		{
			name: "invalid server address",
			mutate: func(config *model.ClientConfiguration) {
				config.ServerAddress = "broken"
			},
		},
		{
			name: "network client address",
			mutate: func(config *model.ClientConfiguration) {
				config.ClientAddress = "10.203.0.0/32"
			},
		},
		{
			name: "public allowed IP",
			mutate: func(config *model.ClientConfiguration) {
				config.AllowedIPs = append(config.AllowedIPs, "8.8.8.0/24")
			},
		},
		{
			name: "missing server route",
			mutate: func(config *model.ClientConfiguration) {
				config.AllowedIPs = []string{"192.168.71.0/24"}
			},
		},
		{
			name: "zero server key",
			mutate: func(config *model.ClientConfiguration) {
				config.ServerPublicKey = testKey(0)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validClientConfiguration()
			test.mutate(&config)
			if _, err := NormalizeClientConfiguration(config); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestClientUAPI(t *testing.T) {
	plan := validClientPlan()
	config, err := ClientUAPI(plan)
	if err != nil {
		t.Fatalf("build UAPI: %v", err)
	}
	for _, expected := range []string{
		"private_key=",
		"replace_peers=true",
		"public_key=",
		"endpoint=127.0.0.1:51821",
		"persistent_keepalive_interval=25",
		"replace_allowed_ips=true",
		"allowed_ip=10.203.0.1/32",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("UAPI config missing %q:\n%s", expected, config)
		}
	}
}

func TestNormalizeDeviceRegistration(t *testing.T) {
	configuration := validClientConfiguration()
	registration := model.DeviceRegistration{
		Device: model.Device{
			ID:             configuration.DeviceID,
			PublicKey:      testKey(3),
			OverlayAddress: configuration.ClientAddress,
			Enabled:        true,
		},
		Configuration: configuration,
	}
	if _, err := NormalizeDeviceRegistration(registration, testKey(3)); err != nil {
		t.Fatalf("normalize registration: %v", err)
	}
	registration.Device.PublicKey = testKey(4)
	if _, err := NormalizeDeviceRegistration(registration, testKey(3)); err == nil {
		t.Fatal("mismatched registration public key was accepted")
	}
}

func validClientPlan() model.ClientPlan {
	return model.ClientPlan{
		Mode:            "relay",
		PrivateKey:      testKey(1),
		ClientAddress:   "10.203.0.2/32",
		ServerPublicKey: testKey(2),
		Endpoint:        "127.0.0.1:51821",
		AllowedIPs:      []string{"10.203.0.1/32"},
	}
}

func validClientConfiguration() model.ClientConfiguration {
	return model.ClientConfiguration{
		DeviceID:        "device-1",
		ServerPublicKey: testKey(2),
		ServerAddress:   "10.203.0.1/24",
		ClientAddress:   "10.203.0.2/32",
		ListenPort:      51820,
		AllowedIPs:      []string{"10.203.0.1/32", "192.168.71.0/24"},
	}
}

func testKey(value byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}
