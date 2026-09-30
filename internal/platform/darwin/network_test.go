//go:build darwin

package darwin

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestNetworkConfiguratorApplyAndRemoveOrder(t *testing.T) {
	var events []string
	interfaces := &recordingInterfaceConfigurator{events: &events}
	routes := &recordingRouteConfigurator{events: &events}
	configurator := newNetworkConfigurator(interfaces, routes)
	plan := networkTestPlan()

	if err := configurator.Apply(context.Background(), "utun8", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := configurator.Remove(context.Background(), "utun8"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	expected := []string{
		"interface.configure",
		"route.add:10.203.0.1/32",
		"route.add:192.168.71.0/24",
		"route.delete:192.168.71.0/24",
		"route.delete:10.203.0.1/32",
		"interface.remove",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
	if len(configurator.applied) != 0 {
		t.Fatalf("applied state was not removed: %#v", configurator.applied)
	}
}

func TestNetworkConfiguratorRollsBackPartialApply(t *testing.T) {
	var events []string
	routeFailure := errors.New("route conflict")
	configurator := newNetworkConfigurator(
		&recordingInterfaceConfigurator{events: &events},
		&recordingRouteConfigurator{
			events:    &events,
			failAddAt: 2,
			addErr:    routeFailure,
		},
	)

	err := configurator.Apply(context.Background(), "utun8", networkTestPlan())
	if !errors.Is(err, routeFailure) {
		t.Fatalf("apply error = %v", err)
	}
	expected := []string{
		"interface.configure",
		"route.add:10.203.0.1/32",
		"route.add:192.168.71.0/24",
		"route.delete:10.203.0.1/32",
		"interface.remove",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
	if len(configurator.applied) != 0 {
		t.Fatalf("failed apply retained state: %#v", configurator.applied)
	}
}

func TestNetworkConfiguratorRequiresRemoveBeforeApply(t *testing.T) {
	var events []string
	configurator := newNetworkConfigurator(
		&recordingInterfaceConfigurator{events: &events},
		&recordingRouteConfigurator{events: &events},
	)
	plan := networkTestPlan()
	if err := configurator.Apply(context.Background(), "utun8", plan); err != nil {
		t.Fatalf("initial apply: %v", err)
	}

	events = nil
	if err := configurator.Apply(context.Background(), "utun8", plan); err == nil ||
		model.AsError(err).Code != model.ErrorFailedPrecondition {
		t.Fatalf("second apply error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("second apply changed network: %v", events)
	}
	if err := configurator.Remove(context.Background(), "utun8"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := configurator.Apply(context.Background(), "utun8", plan); err != nil {
		t.Fatalf("apply after remove: %v", err)
	}
}

func TestNetworkConfiguratorRetainsFailedRollbackForRetry(t *testing.T) {
	var events []string
	cleanupError := errors.New("interface cleanup failed")
	interfaces := &recordingInterfaceConfigurator{
		events:    &events,
		removeErr: cleanupError,
	}
	configurator := newNetworkConfigurator(
		interfaces,
		&recordingRouteConfigurator{
			events:    &events,
			failAddAt: 1,
			addErr:    errors.New("route failed"),
		},
	)
	plan := networkTestPlan()

	err := configurator.Apply(context.Background(), "utun8", plan)
	if !errors.Is(err, cleanupError) {
		t.Fatalf("apply error = %v", err)
	}
	if len(configurator.applied) != 1 {
		t.Fatalf("failed rollback state = %#v", configurator.applied)
	}

	interfaces.removeErr = nil
	if err := configurator.Remove(context.Background(), "utun8"); err != nil {
		t.Fatalf("retry remove: %v", err)
	}
	if len(configurator.applied) != 0 {
		t.Fatalf("retry did not clear state: %#v", configurator.applied)
	}
}

func TestNetworkConfiguratorRetriesOnlyFailedCleanupSteps(t *testing.T) {
	var events []string
	routeError := errors.New("route delete failed")
	routes := &recordingRouteConfigurator{events: &events}
	configurator := newNetworkConfigurator(
		&recordingInterfaceConfigurator{events: &events},
		routes,
	)
	plan := networkTestPlan()
	if err := configurator.Apply(context.Background(), "utun8", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	events = nil
	routes.deleteErrors = map[string]error{"10.203.0.1/32": routeError}
	if err := configurator.Remove(
		context.Background(),
		"utun8",
	); !errors.Is(err, routeError) {
		t.Fatalf("remove error = %v", err)
	}
	expected := []string{
		"route.delete:192.168.71.0/24",
		"route.delete:10.203.0.1/32",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}

	events = nil
	routes.deleteErrors = nil
	if err := configurator.Remove(context.Background(), "utun8"); err != nil {
		t.Fatalf("retry remove: %v", err)
	}
	expected = []string{
		"route.delete:10.203.0.1/32",
		"interface.remove",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("retry events = %#v, want %#v", events, expected)
	}
}

func TestNetworkConfiguratorPersistsAndClearsActiveState(t *testing.T) {
	var events []string
	store := &recordingNetworkStateStore{}
	configurator := newNetworkConfiguratorWithState(
		&recordingInterfaceConfigurator{events: &events},
		&recordingRouteConfigurator{events: &events},
		store,
		&recordingRecoveryInspector{},
		42,
	)
	plan := networkTestPlan()

	if err := configurator.Apply(context.Background(), "utun8", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.saved) != 2 {
		t.Fatalf("saved states = %#v", store.saved)
	}
	saved := store.saved[len(store.saved)-1]
	if saved.PID != 42 ||
		saved.Interface != "utun8" ||
		saved.Address != "10.203.0.2/32" ||
		!slices.Equal(saved.Routes, plan.AllowedIPs) {
		t.Fatalf("saved state = %#v", saved)
	}

	if err := configurator.Remove(context.Background(), "utun8"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if store.cleared != 1 {
		t.Fatalf("state clear count = %d", store.cleared)
	}
}

func TestNetworkConfiguratorRecoveryClearsStateWhenInterfaceIsGone(t *testing.T) {
	store := &recordingNetworkStateStore{loaded: persistedNetworkTestState()}
	configurator := newNetworkConfiguratorWithState(
		&recordingInterfaceConfigurator{events: &[]string{}},
		&recordingRouteConfigurator{events: &[]string{}},
		store,
		&recordingRecoveryInspector{},
		99,
	)

	if err := configurator.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if store.cleared != 1 {
		t.Fatalf("state clear count = %d", store.cleared)
	}
}

func TestNetworkConfiguratorRecoveryRejectsLiveOwner(t *testing.T) {
	store := &recordingNetworkStateStore{loaded: persistedNetworkTestState()}
	configurator := newNetworkConfiguratorWithState(
		&recordingInterfaceConfigurator{events: &[]string{}},
		&recordingRouteConfigurator{events: &[]string{}},
		store,
		&recordingRecoveryInspector{ownsAddress: true, processAlive: true},
		99,
	)

	err := configurator.Recover(context.Background())
	if err == nil || model.AsError(err).Code != model.ErrorConflict {
		t.Fatalf("recover error = %v", err)
	}
	if store.cleared != 0 {
		t.Fatalf("live owner state was cleared")
	}
}

func TestNetworkConfiguratorRecoveryCleansDeadOwner(t *testing.T) {
	var events []string
	store := &recordingNetworkStateStore{loaded: persistedNetworkTestState()}
	configurator := newNetworkConfiguratorWithState(
		&recordingInterfaceConfigurator{events: &events},
		&recordingRouteConfigurator{events: &events},
		store,
		&recordingRecoveryInspector{ownsAddress: true},
		99,
	)

	if err := configurator.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	expected := []string{
		"route.delete:192.168.71.0/24",
		"route.delete:10.203.0.1/32",
		"interface.remove",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
	if store.cleared != 1 {
		t.Fatalf("state clear count = %d", store.cleared)
	}
}

func persistedNetworkTestState() *persistedNetworkState {
	return &persistedNetworkState{
		Version:          networkStateVersion,
		PID:              42,
		ProcessStartedAt: 1,
		Executable:       "fncpn",
		OwnerToken:       "00112233445566778899aabbccddeeff",
		Interface:        "utun8",
		Address:          "10.203.0.2/32",
		MTU:              1280,
		Routes:           []string{"10.203.0.1/32", "192.168.71.0/24"},
	}
}

func networkTestPlan() model.ClientPlan {
	return model.ClientPlan{
		ClientAddress: "10.203.0.2/32",
		AllowedIPs:    []string{"10.203.0.1/32", "192.168.71.0/24"},
		MTU:           1280,
	}
}

type recordingInterfaceConfigurator struct {
	events    *[]string
	removeErr error
}

func (*recordingInterfaceConfigurator) Available() bool {
	return true
}

func (c *recordingInterfaceConfigurator) Configure(
	context.Context,
	string,
	netip.Prefix,
	int,
) error {
	*c.events = append(*c.events, "interface.configure")
	return nil
}

func (c *recordingInterfaceConfigurator) Remove(
	context.Context,
	string,
	netip.Prefix,
) error {
	*c.events = append(*c.events, "interface.remove")
	return c.removeErr
}

type recordingRouteConfigurator struct {
	events       *[]string
	addCount     int
	failAddAt    int
	addErr       error
	deleteErrors map[string]error
}

func (*recordingRouteConfigurator) Available() bool {
	return true
}

func (c *recordingRouteConfigurator) Add(
	_ context.Context,
	_ string,
	prefix netip.Prefix,
) error {
	c.addCount++
	*c.events = append(*c.events, "route.add:"+prefix.String())
	if c.addCount == c.failAddAt {
		return c.addErr
	}
	return nil
}

func (c *recordingRouteConfigurator) Delete(
	_ context.Context,
	_ string,
	prefix netip.Prefix,
) error {
	*c.events = append(*c.events, "route.delete:"+prefix.String())
	return c.deleteErrors[prefix.String()]
}

type recordingNetworkStateStore struct {
	loaded  *persistedNetworkState
	saved   []persistedNetworkState
	cleared int
}

func (s *recordingNetworkStateStore) Load() (*persistedNetworkState, error) {
	return s.loaded, nil
}

func (s *recordingNetworkStateStore) Save(state persistedNetworkState) error {
	s.saved = append(s.saved, state)
	s.loaded = &state
	return nil
}

func (s *recordingNetworkStateStore) Clear() error {
	s.cleared++
	s.loaded = nil
	return nil
}

type recordingRecoveryInspector struct {
	processAlive bool
	ownsAddress  bool
}

func (i *recordingRecoveryInspector) ProcessMatches(int, int64, string) bool {
	return i.processAlive
}

func (i *recordingRecoveryInspector) InterfaceHasAddress(
	string,
	netip.Prefix,
) bool {
	if !i.ownsAddress {
		return false
	}
	i.ownsAddress = false
	return true
}

func (*recordingRecoveryInspector) RouteUsesInterface(
	context.Context,
	netip.Prefix,
	string,
) (bool, error) {
	return false, nil
}
