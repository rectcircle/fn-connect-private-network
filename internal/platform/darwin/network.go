//go:build darwin

package darwin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type interfaceConfigurator interface {
	Available() bool
	Configure(context.Context, string, netip.Prefix, int) error
	Remove(context.Context, string, netip.Prefix) error
}

type routeConfigurator interface {
	Available() bool
	Add(context.Context, string, netip.Prefix) error
	Delete(context.Context, string, netip.Prefix) error
}

type appliedNetwork struct {
	address netip.Prefix
	mtu     int
	routes  []netip.Prefix
}

type DarwinNetworkConfigurator struct {
	mu                  sync.Mutex
	interfaces          interfaceConfigurator
	routes              routeConfigurator
	state               networkStateStore
	inspector           recoveryInspector
	pid                 int
	processStartedAt    int64
	executable          string
	ownerToken          string
	ownerUID            uint32
	ownerLease          *os.File
	initializationError error
	applied             map[string]appliedNetwork
}

func NewNetworkConfigurator(stateDirectory string) *DarwinNetworkConfigurator {
	if stateDirectory == "" {
		stateDirectory = "/var/db/fncpn"
	}
	runner := execCommandRunner{}
	configurator := newNetworkConfiguratorWithState(
		commandInterfaceConfigurator{runner: runner},
		commandRouteConfigurator{runner: runner},
		fileNetworkStateStore{
			path: filepath.Join(stateDirectory, filepath.Base(defaultNetworkStatePath)),
		},
		systemRecoveryInspector{},
		os.Getpid(),
	)
	lease, err := acquireOwnerLease(stateDirectory, configurator.ownerToken)
	configurator.ownerLease = lease
	configurator.initializationError = err
	return configurator
}

func newNetworkConfigurator(
	interfaces interfaceConfigurator,
	routes routeConfigurator,
) *DarwinNetworkConfigurator {
	return newNetworkConfiguratorWithState(
		interfaces,
		routes,
		discardNetworkStateStore{},
		systemRecoveryInspector{},
		os.Getpid(),
	)
}

func newNetworkConfiguratorWithState(
	interfaces interfaceConfigurator,
	routes routeConfigurator,
	state networkStateStore,
	inspector recoveryInspector,
	pid int,
) *DarwinNetworkConfigurator {
	startedAt, executable, _ := processIdentity(pid)
	if startedAt == 0 {
		startedAt = 1
	}
	if executable == "" {
		executable = "unknown"
	}
	ownerToken, _ := newNetworkOwnerToken()
	return &DarwinNetworkConfigurator{
		interfaces:       interfaces,
		routes:           routes,
		state:            state,
		inspector:        inspector,
		pid:              pid,
		processStartedAt: startedAt,
		executable:       executable,
		ownerToken:       ownerToken,
		applied:          make(map[string]appliedNetwork),
	}
}

func (c *DarwinNetworkConfigurator) Available() bool {
	return c != nil &&
		c.interfaces != nil &&
		c.interfaces.Available() &&
		c.routes != nil &&
		c.routes.Available() &&
		c.state != nil &&
		c.inspector != nil &&
		c.pid > 1 &&
		c.processStartedAt > 0 &&
		c.executable != "" &&
		c.ownerToken != "" &&
		c.initializationError == nil
}

func (c *DarwinNetworkConfigurator) SetOwnerUID(uid uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ownerUID = uid
}

func (c *DarwinNetworkConfigurator) Recover(ctx context.Context) error {
	state, err := c.state.Load()
	if err != nil || state == nil {
		return err
	}
	network, err := networkFromPersistedState(*state)
	if err != nil {
		return err
	}
	if c.inspector.ProcessMatches(
		state.PID,
		state.ProcessStartedAt,
		state.Executable,
	) {
		return model.NewError(
			model.ErrorConflict,
			"another privileged daemon owns the active network state",
			false,
		)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.applied[state.Interface] = network
	interfacePresent := c.inspector.InterfaceHasAddress(state.Interface, network.address)
	if interfacePresent {
		if err := c.removeLocked(ctx, state.Interface); err != nil {
			return errors.Join(err, c.persistLocked(state.Interface))
		}
	} else {
		var cleanupErrors []error
		var remaining []netip.Prefix
		for index := len(network.routes) - 1; index >= 0; index-- {
			route := network.routes[index]
			exists, inspectErr := c.inspector.RouteUsesInterface(
				ctx,
				route,
				state.Interface,
			)
			if inspectErr != nil {
				cleanupErrors = append(cleanupErrors, inspectErr)
				remaining = append(remaining, route)
				continue
			}
			if exists {
				if err := c.routes.Delete(ctx, state.Interface, route); err != nil {
					cleanupErrors = append(cleanupErrors, err)
					remaining = append(remaining, route)
				}
			}
		}
		if len(remaining) > 0 {
			slices.Reverse(remaining)
			network.routes = remaining
			c.applied[state.Interface] = network
			return errors.Join(
				errors.Join(cleanupErrors...),
				c.persistLocked(state.Interface),
			)
		}
		delete(c.applied, state.Interface)
	}
	if c.inspector.InterfaceHasAddress(state.Interface, network.address) {
		return errors.New("network interface address remains after cleanup")
	}
	for _, route := range network.routes {
		exists, err := c.inspector.RouteUsesInterface(ctx, route, state.Interface)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("route %s remains after cleanup", route)
		}
	}
	return c.state.Clear()
}

func (c *DarwinNetworkConfigurator) Apply(
	ctx context.Context,
	interfaceName string,
	plan model.ClientPlan,
) error {
	desired, err := networkFromPlan(plan)
	if err != nil {
		return err
	}
	if !c.Available() {
		return unavailableNetworkError()
	}
	if !validUTUNName(interfaceName) {
		return errors.New("invalid utun interface name")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.applied[interfaceName]; ok {
		return model.NewError(
			model.ErrorFailedPrecondition,
			"remove the active network state before applying another plan",
			false,
		)
	}
	if err := c.persistNetwork(interfaceName, desired); err != nil {
		return fmt.Errorf("persist network cleanup intent: %w", err)
	}
	if err := c.applyNew(ctx, interfaceName, desired); err != nil {
		if _, ok := c.applied[interfaceName]; ok {
			return errors.Join(err, c.persistLocked(interfaceName))
		}
		return errors.Join(err, c.state.Clear())
	}
	if err := c.persistLocked(interfaceName); err != nil {
		cleanupErr := c.removeLocked(context.Background(), interfaceName)
		var stateErr error
		if cleanupErr == nil {
			stateErr = c.state.Clear()
		} else if _, ok := c.applied[interfaceName]; ok {
			stateErr = c.persistLocked(interfaceName)
		}
		return errors.Join(
			fmt.Errorf("persist network state: %w", err),
			cleanupErr,
			stateErr,
		)
	}
	return nil
}

func (c *DarwinNetworkConfigurator) Remove(
	ctx context.Context,
	interfaceName string,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.removeLocked(ctx, interfaceName); err != nil {
		if _, ok := c.applied[interfaceName]; ok {
			return errors.Join(err, c.persistLocked(interfaceName))
		}
		return err
	}
	return c.state.Clear()
}

func (c *DarwinNetworkConfigurator) removeLocked(
	_ context.Context,
	interfaceName string,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	current, ok := c.applied[interfaceName]
	if !ok {
		return nil
	}

	var cleanupErrors []error
	var remainingRoutes []netip.Prefix
	for index := len(current.routes) - 1; index >= 0; index-- {
		prefix := current.routes[index]
		if err := c.routes.Delete(cleanupContext, interfaceName, prefix); err != nil {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("delete route %s: %w", prefix, err),
			)
			remainingRoutes = append(remainingRoutes, prefix)
		}
	}
	if len(remainingRoutes) > 0 {
		slices.Reverse(remainingRoutes)
		current.routes = remainingRoutes
		c.applied[interfaceName] = current
		return errors.Join(cleanupErrors...)
	}
	current.routes = nil
	c.applied[interfaceName] = current
	if err := c.interfaces.Remove(
		cleanupContext,
		interfaceName,
		current.address,
	); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove interface address: %w", err))
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		return err
	}
	delete(c.applied, interfaceName)
	return nil
}

func (c *DarwinNetworkConfigurator) applyNew(
	ctx context.Context,
	interfaceName string,
	desired appliedNetwork,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := c.interfaces.Configure(
		ctx,
		interfaceName,
		desired.address,
		desired.mtu,
	); err != nil {
		return fmt.Errorf("configure interface: %w", err)
	}

	var addedRoutes []netip.Prefix
	rollback := func(cause error) error {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var rollbackErrors []error
		var remainingRoutes []netip.Prefix
		for index := len(addedRoutes) - 1; index >= 0; index-- {
			prefix := addedRoutes[index]
			if err := c.routes.Delete(cleanupContext, interfaceName, prefix); err != nil {
				rollbackErrors = append(
					rollbackErrors,
					fmt.Errorf("delete route %s: %w", prefix, err),
				)
				remainingRoutes = append(remainingRoutes, prefix)
			}
		}
		if len(remainingRoutes) > 0 {
			slices.Reverse(remainingRoutes)
			residual := desired
			residual.routes = remainingRoutes
			c.applied[interfaceName] = residual
			return errors.Join(append([]error{cause}, rollbackErrors...)...)
		}
		if err := c.interfaces.Remove(
			cleanupContext,
			interfaceName,
			desired.address,
		); err != nil {
			rollbackErrors = append(
				rollbackErrors,
				fmt.Errorf("remove interface address: %w", err),
			)
			residual := desired
			residual.routes = nil
			c.applied[interfaceName] = residual
		} else {
			delete(c.applied, interfaceName)
		}
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}

	for _, prefix := range desired.routes {
		if err := contextError(ctx); err != nil {
			return rollback(err)
		}
		if err := c.routes.Add(ctx, interfaceName, prefix); err != nil {
			return rollback(fmt.Errorf("add route %s: %w", prefix, err))
		}
		addedRoutes = append(addedRoutes, prefix)
	}
	c.applied[interfaceName] = desired
	return nil
}

func networkFromPlan(plan model.ClientPlan) (appliedNetwork, error) {
	address, err := netip.ParsePrefix(plan.ClientAddress)
	if err != nil || !address.Addr().Is4() || address.Bits() != 32 {
		return appliedNetwork{}, errors.New("client address must be an IPv4 host prefix")
	}
	routes := make([]netip.Prefix, 0, len(plan.AllowedIPs))
	for _, value := range plan.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return appliedNetwork{}, fmt.Errorf("invalid route %q", value)
		}
		routes = append(routes, prefix.Masked())
	}
	return appliedNetwork{
		address: address,
		mtu:     plan.MTU,
		routes:  routes,
	}, nil
}

func (c *DarwinNetworkConfigurator) persistLocked(interfaceName string) error {
	network, ok := c.applied[interfaceName]
	if !ok {
		return errors.New("network state is not active")
	}
	routes := make([]string, len(network.routes))
	for index, prefix := range network.routes {
		routes[index] = prefix.String()
	}
	return c.persistNetwork(interfaceName, network)
}

func (c *DarwinNetworkConfigurator) persistNetwork(
	interfaceName string,
	network appliedNetwork,
) error {
	routes := make([]string, len(network.routes))
	for index, prefix := range network.routes {
		routes[index] = prefix.String()
	}
	return c.state.Save(persistedNetworkState{
		Version:          networkStateVersion,
		PID:              c.pid,
		ProcessStartedAt: c.processStartedAt,
		Executable:       c.executable,
		OwnerToken:       c.ownerToken,
		OwnerUID:         c.ownerUID,
		Interface:        interfaceName,
		Address:          network.address.String(),
		MTU:              network.mtu,
		Routes:           routes,
	})
}

func networkFromPersistedState(
	state persistedNetworkState,
) (appliedNetwork, error) {
	if err := validatePersistedNetworkState(state); err != nil {
		return appliedNetwork{}, err
	}
	address, _ := netip.ParsePrefix(state.Address)
	routes := make([]netip.Prefix, len(state.Routes))
	for index, value := range state.Routes {
		routes[index], _ = netip.ParsePrefix(value)
	}
	return appliedNetwork{
		address: address,
		mtu:     state.MTU,
		routes:  routes,
	}, nil
}

func validUTUNName(name string) bool {
	if len(name) <= len("utun") || len(name) > 15 || !strings.HasPrefix(name, "utun") {
		return false
	}
	for _, character := range name[len("utun"):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
