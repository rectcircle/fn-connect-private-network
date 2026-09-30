package server

import (
	"reflect"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestValidateSettings(t *testing.T) {
	valid := model.ServerSettings{
		OverlayCIDR: "10.203.0.0/24",
		ListenPort:  51820,
		LANCIDRs:    []string{"192.168.71.0/24"},
	}
	if err := ValidateSettings(valid); err != nil {
		t.Fatalf("validate settings: %v", err)
	}

	tests := []model.ServerSettings{
		{OverlayCIDR: "8.8.8.0/24", ListenPort: 51820},
		{OverlayCIDR: "10.203.0.1/24", ListenPort: 51820},
		{OverlayCIDR: "10.203.0.0/30", ListenPort: 51820},
		{
			OverlayCIDR: "10.203.0.0/24",
			ListenPort:  51820,
			LANCIDRs:    []string{"10.203.0.128/25"},
		},
		{
			OverlayCIDR: "10.203.0.0/24",
			ListenPort:  51820,
			LANCIDRs:    []string{"8.8.8.0/24"},
		},
	}
	for _, settings := range tests {
		if err := ValidateSettings(settings); err == nil {
			t.Fatalf("settings unexpectedly accepted: %+v", settings)
		}
	}
}

func TestValidatePublicKeyRejectsAllZero(t *testing.T) {
	if err := ValidatePublicKey(testPublicKey(0)); err == nil {
		t.Fatal("zero public key unexpectedly accepted")
	}
}

func TestOverlayAddresses(t *testing.T) {
	server, err := ServerAddress("10.203.0.0/29")
	if err != nil {
		t.Fatalf("server address: %v", err)
	}
	if server != "10.203.0.1/29" {
		t.Fatalf("server address = %q", server)
	}

	addresses, err := AllocateAddresses("10.203.0.0/29", 3)
	if err != nil {
		t.Fatalf("allocate addresses: %v", err)
	}
	want := []string{"10.203.0.2/32", "10.203.0.3/32", "10.203.0.4/32"}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("addresses = %v, want %v", addresses, want)
	}
}
