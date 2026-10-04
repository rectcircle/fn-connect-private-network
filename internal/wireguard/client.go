package wireguard

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func NormalizeClientConfiguration(
	config model.ClientConfiguration,
) (model.ClientConfiguration, error) {
	config.DeviceID = strings.TrimSpace(config.DeviceID)
	if config.DeviceID == "" {
		return model.ClientConfiguration{}, errors.New("device ID is required")
	}
	config.ServerPublicKey = strings.TrimSpace(config.ServerPublicKey)
	if err := ValidateKey(config.ServerPublicKey); err != nil {
		return model.ClientConfiguration{}, fmt.Errorf("invalid server public key: %w", err)
	}

	serverAddress, err := netip.ParsePrefix(config.ServerAddress)
	if err != nil ||
		!serverAddress.Addr().Is4() ||
		serverAddress.Bits() < 16 ||
		serverAddress.Bits() > 29 {
		return model.ClientConfiguration{}, errors.New(
			"server address must be an IPv4 /16-/29 prefix",
		)
	}
	overlay := serverAddress.Masked()
	if serverAddress.Addr() != overlay.Addr().Next() {
		return model.ClientConfiguration{}, errors.New(
			"server address must be the first usable overlay address",
		)
	}
	if !overlay.Addr().IsPrivate() && !sharedIPv4Prefix.Contains(overlay.Addr()) {
		return model.ClientConfiguration{}, errors.New(
			"server address must use RFC1918 or 100.64.0.0/10 space",
		)
	}

	clientAddress, err := netip.ParsePrefix(config.ClientAddress)
	if err != nil ||
		!clientAddress.Addr().Is4() ||
		clientAddress.Bits() != 32 ||
		!overlay.Contains(clientAddress.Addr()) ||
		clientAddress.Addr() == overlay.Addr() ||
		clientAddress.Addr() == serverAddress.Addr() ||
		!overlay.Contains(clientAddress.Addr().Next()) {
		return model.ClientConfiguration{}, errors.New(
			"client address must be a usable overlay IPv4 host",
		)
	}
	if config.ListenPort == 0 {
		return model.ClientConfiguration{}, errors.New("listen port is required")
	}
	if len(config.AllowedIPs) == 0 || len(config.AllowedIPs) > 64 {
		return model.ClientConfiguration{}, errors.New(
			"allowed IPs must contain 1-64 prefixes",
		)
	}

	config.AllowedIPs = append([]string(nil), config.AllowedIPs...)
	serverRoute := netip.PrefixFrom(serverAddress.Addr(), 32)
	hasServerRoute := false
	seen := make(map[netip.Prefix]struct{}, len(config.AllowedIPs))
	for index, value := range config.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
			return model.ClientConfiguration{}, fmt.Errorf(
				"invalid allowed IP %q",
				value,
			)
		}
		if prefix == serverRoute {
			hasServerRoute = true
		} else if _, err := ParsePrivateIPv4Prefix(value); err != nil {
			return model.ClientConfiguration{}, fmt.Errorf(
				"invalid allowed IP %q: %w",
				value,
				err,
			)
		}
		if _, exists := seen[prefix]; exists {
			return model.ClientConfiguration{}, fmt.Errorf(
				"duplicate allowed IP %q",
				value,
			)
		}
		seen[prefix] = struct{}{}
		config.AllowedIPs[index] = prefix.String()
	}
	if !hasServerRoute {
		return model.ClientConfiguration{}, errors.New(
			"allowed IPs must include the server host route",
		)
	}

	config.ServerAddress = serverAddress.String()
	config.ClientAddress = clientAddress.String()
	return config, nil
}

func NormalizeDeviceRegistration(
	registration model.DeviceRegistration,
	expectedPublicKey string,
) (model.DeviceRegistration, error) {
	expectedPublicKey = strings.TrimSpace(expectedPublicKey)
	if err := ValidateKey(expectedPublicKey); err != nil {
		return model.DeviceRegistration{}, fmt.Errorf(
			"invalid expected client public key: %w",
			err,
		)
	}
	if !registration.Device.Enabled {
		return model.DeviceRegistration{}, errors.New("registered device is disabled")
	}
	if strings.TrimSpace(registration.Device.ID) == "" ||
		registration.Device.ID != registration.Configuration.DeviceID {
		return model.DeviceRegistration{}, errors.New(
			"registration device ID does not match its configuration",
		)
	}
	registration.Device.PublicKey = strings.TrimSpace(registration.Device.PublicKey)
	if registration.Device.PublicKey != expectedPublicKey {
		return model.DeviceRegistration{}, errors.New(
			"registration public key does not match this client",
		)
	}
	configuration, err := NormalizeClientConfiguration(registration.Configuration)
	if err != nil {
		return model.DeviceRegistration{}, err
	}
	deviceAddress, err := netip.ParsePrefix(registration.Device.OverlayAddress)
	if err != nil || deviceAddress.Bits() != 32 ||
		deviceAddress.String() != configuration.ClientAddress {
		return model.DeviceRegistration{}, errors.New(
			"registration device address does not match its configuration",
		)
	}
	registration.Configuration = configuration
	registration.Device.OverlayAddress = deviceAddress.String()
	return registration, nil
}

func NormalizeClientPlan(plan model.ClientPlan) (model.ClientPlan, error) {
	if plan.Mode != "direct" && plan.Mode != "relay" {
		return model.ClientPlan{}, errors.New("mode must be direct or relay")
	}
	privateKey, err := decodeKey(plan.PrivateKey)
	if err != nil {
		return model.ClientPlan{}, fmt.Errorf("invalid private key: %w", err)
	}
	publicKey, err := decodeKey(plan.ServerPublicKey)
	if err != nil {
		return model.ClientPlan{}, fmt.Errorf("invalid server public key: %w", err)
	}
	if string(privateKey) == string(publicKey) {
		return model.ClientPlan{}, errors.New("client and server keys must differ")
	}

	clientAddress, err := ParsePrivateOrSharedIPv4Prefix(plan.ClientAddress)
	if err != nil || clientAddress.Bits() != 32 {
		return model.ClientPlan{}, errors.New(
			"client address must be a private or shared IPv4 host prefix",
		)
	}
	host, portText, err := net.SplitHostPort(plan.Endpoint)
	if err != nil {
		return model.ClientPlan{}, errors.New("invalid endpoint")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return model.ClientPlan{}, errors.New("invalid endpoint port")
	}
	if plan.Mode == "relay" {
		address, err := netip.ParseAddr(strings.Trim(host, "[]"))
		if err != nil || !address.IsLoopback() {
			return model.ClientPlan{}, errors.New("relay endpoint must use a loopback address")
		}
	} else {
		address, err := netip.ParseAddr(strings.Trim(host, "[]"))
		if err != nil || !IsPublicIPv6(address) {
			return model.ClientPlan{}, errors.New(
				"direct endpoint must use a public global-unicast IPv6 literal",
			)
		}
	}

	if len(plan.AllowedIPs) == 0 || len(plan.AllowedIPs) > 64 {
		return model.ClientPlan{}, errors.New("allowed IPs must contain 1-64 prefixes")
	}
	plan.AllowedIPs = append([]string(nil), plan.AllowedIPs...)
	seen := make(map[netip.Prefix]struct{}, len(plan.AllowedIPs))
	for index, value := range plan.AllowedIPs {
		prefix, err := ParsePrivateOrSharedIPv4Prefix(value)
		if err != nil {
			return model.ClientPlan{}, fmt.Errorf("invalid allowed IP %q: %w", value, err)
		}
		if _, exists := seen[prefix]; exists {
			return model.ClientPlan{}, fmt.Errorf("duplicate allowed IP %q", value)
		}
		seen[prefix] = struct{}{}
		plan.AllowedIPs[index] = prefix.String()
	}
	if plan.MTU == 0 {
		plan.MTU = 1280
	}
	if plan.MTU < 576 || plan.MTU > 1420 {
		return model.ClientPlan{}, errors.New("MTU must be between 576 and 1420")
	}
	plan.ClientAddress = clientAddress.String()
	return plan, nil
}

func IsPublicIPv6(address netip.Addr) bool {
	if !address.Is6() ||
		address.Is4In6() ||
		address.Zone() != "" ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsUnspecified() ||
		address.IsLinkLocalUnicast() ||
		address.IsMulticast() {
		return false
	}
	documentation := netip.MustParsePrefix("2001:db8::/32")
	return !documentation.Contains(address)
}

func ClientUAPI(plan model.ClientPlan) (string, error) {
	normalized, err := NormalizeClientPlan(plan)
	if err != nil {
		return "", err
	}
	privateKey, _ := decodeKey(normalized.PrivateKey)
	publicKey, _ := decodeKey(normalized.ServerPublicKey)

	var builder strings.Builder
	fmt.Fprintf(&builder, "private_key=%s\n", hex.EncodeToString(privateKey))
	builder.WriteString("replace_peers=true\n")
	fmt.Fprintf(&builder, "public_key=%s\n", hex.EncodeToString(publicKey))
	fmt.Fprintf(&builder, "endpoint=%s\n", normalized.Endpoint)
	builder.WriteString("persistent_keepalive_interval=25\n")
	builder.WriteString("replace_allowed_ips=true\n")
	for _, prefix := range normalized.AllowedIPs {
		fmt.Fprintf(&builder, "allowed_ip=%s\n", prefix)
	}
	return builder.String(), nil
}
