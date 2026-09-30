//go:build darwin

package darwin

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"syscall"
	"testing"

	"golang.org/x/net/route"
)

func TestCommandInterfaceConfigurator(t *testing.T) {
	runner := &recordingCommandRunner{available: true}
	configurator := commandInterfaceConfigurator{
		runner: runner,
		hasAddress: func(string, netip.Prefix) (bool, error) { return true, nil },
	}
	address := netip.MustParsePrefix("10.203.0.2/32")

	if !configurator.Available() {
		t.Fatal("interface configurator is unavailable")
	}
	if err := configurator.Configure(
		context.Background(),
		"utun8",
		address,
		1280,
	); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := configurator.Remove(
		context.Background(),
		"utun8",
		address,
	); err != nil {
		t.Fatalf("remove: %v", err)
	}

	expected := []recordedCommand{
		{
			path: ifconfigPath,
			arguments: []string{
				"utun8", "inet", "10.203.0.2", "10.203.0.2",
				"netmask", "255.255.255.255", "mtu", "1280", "up",
			},
		},
		{
			path:      ifconfigPath,
			arguments: []string{"utun8", "inet", "10.203.0.2", "-alias"},
		},
	}
	if !commandsEqual(runner.commands, expected) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, expected)
	}
}

func TestCommandRouteConfigurator(t *testing.T) {
	runner := &recordingCommandRunner{available: true}
	configurator := commandRouteConfigurator{
		runner: runner,
		usesInterface: func(context.Context, netip.Prefix, string) (bool, error) { return true, nil },
	}
	prefix := netip.MustParsePrefix("192.168.71.9/24")

	if !configurator.Available() {
		t.Fatal("route configurator is unavailable")
	}
	if err := configurator.Add(context.Background(), "utun8", prefix); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := configurator.Delete(context.Background(), "utun8", prefix); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := configurator.Add(
		context.Background(),
		"utun8",
		netip.MustParsePrefix("fd00:71::9/64"),
	); err != nil {
		t.Fatalf("add IPv6: %v", err)
	}

	expected := []recordedCommand{
		{
			path: routePath,
			arguments: []string{
				"-n", "add", "-net", "192.168.71.0/24", "-interface", "utun8",
			},
		},
		{
			path: routePath,
			arguments: []string{
				"-n", "delete", "-net", "192.168.71.0/24", "-interface", "utun8",
			},
		},
		{
			path: routePath,
			arguments: []string{
				"-n", "add", "-inet6", "-net", "fd00:71::/64",
				"-interface", "utun8",
			},
		},
	}
	if !commandsEqual(runner.commands, expected) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, expected)
	}
}

type recordedCommand struct {
	path      string
	arguments []string
}

type recordingCommandRunner struct {
	available bool
	commands  []recordedCommand
	err error
}

func (r *recordingCommandRunner) Available(string) bool {
	return r.available
}

func (r *recordingCommandRunner) Run(
	_ context.Context,
	path string,
	arguments ...string,
) error {
	r.commands = append(r.commands, recordedCommand{
		path:      path,
		arguments: slices.Clone(arguments),
	})
	return r.err
}

func commandsEqual(left, right []recordedCommand) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].path != right[index].path ||
			!slices.Equal(left[index].arguments, right[index].arguments) {
			return false
		}
	}
	return true
}

func TestCleanupConvergesOnMissingResources(t *testing.T) {
	for _, scenario := range []struct{
		name string
		present bool
		disappears bool
		inspectErr error
		wantError bool
	}{
		{name: "already_absent"},
		{name: "disappears_during_delete", present: true, disappears: true},
		{name: "permission_failure", present: true, wantError: true},
		{name: "inspection_failure", inspectErr: errors.New("sysctl failed"), wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, addressOnly := range []bool{false, true} {
				runner := &recordingCommandRunner{err: errors.New("delete failed")}
				present := func() (bool, error) {
					return scenario.present && !(scenario.disappears && len(runner.commands) > 0), scenario.inspectErr
				}
				routes := commandRouteConfigurator{
					runner: runner,
					usesInterface: func(context.Context, netip.Prefix, string) (bool, error) { return present() },
				}
				addresses := commandInterfaceConfigurator{
					runner: runner, hasAddress: func(string, netip.Prefix) (bool, error) { return present() },
				}
				for range 2 {
					var err error
					if addressOnly {
						err = addresses.Remove(context.Background(), "utun8", netip.MustParsePrefix("10.203.0.2/32"))
					} else {
						err = routes.Delete(context.Background(), "utun8", netip.MustParsePrefix("192.168.71.0/24"))
					}
					if (err != nil) != scenario.wantError {
						t.Fatalf("addressOnly=%v error=%v", addressOnly, err)
					}
				}
				if !scenario.present && len(runner.commands) != 0 {
					t.Fatal("attempted to delete an absent or unverified resource")
				}
			}
		})
	}
}

func TestRouteMatchRequiresExactPrefixAndInterface(t *testing.T) {
	entry := &route.RouteMessage{
		Index: 8,
		Addrs: []route.Addr{
			syscall.RTAX_DST: &route.Inet4Addr{IP: [4]byte{192, 168, 0, 0}},
			syscall.RTAX_NETMASK: &route.Inet4Addr{IP: [4]byte{255, 255, 0, 0}},
		},
	}
	if !routeMatches(entry, netip.MustParsePrefix("192.168.0.0/16"), 8) {
		t.Fatal("exact route did not match")
	}
	if routeMatches(entry, netip.MustParsePrefix("192.168.71.0/24"), 8) ||
		routeMatches(entry, netip.MustParsePrefix("192.168.0.0/16"), 9) {
		t.Fatal("broader route or different interface was matched")
	}
}
