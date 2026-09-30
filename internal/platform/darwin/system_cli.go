//go:build darwin

package darwin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ifconfigPath = "/sbin/ifconfig"
	routePath    = "/sbin/route"
)

type commandRunner interface {
	Available(string) bool
	Run(context.Context, string, ...string) error
}

type execCommandRunner struct{}

func (execCommandRunner) Available(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func (execCommandRunner) Run(
	ctx context.Context,
	path string,
	arguments ...string,
) error {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if len(message) > 4096 {
		message = message[:4096]
	}
	if message == "" {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return fmt.Errorf("%s: %w: %s", filepath.Base(path), err, message)
}

type commandInterfaceConfigurator struct {
	runner commandRunner
	hasAddress func(string, netip.Prefix) (bool, error)
}

func (c commandInterfaceConfigurator) Available() bool {
	return c.runner != nil && c.runner.Available(ifconfigPath)
}

func (c commandInterfaceConfigurator) Configure(
	ctx context.Context,
	interfaceName string,
	address netip.Prefix,
	mtu int,
) error {
	value := address.Addr().String()
	return c.runner.Run(
		ctx,
		ifconfigPath,
		interfaceName,
		"inet",
		value,
		value,
		"netmask",
		"255.255.255.255",
		"mtu",
		strconv.Itoa(mtu),
		"up",
	)
}

func (c commandInterfaceConfigurator) Remove(
	ctx context.Context,
	interfaceName string,
	address netip.Prefix,
) error {
	inspect := c.hasAddress
	if inspect == nil {
		inspect = interfaceHasAddress
	}
	present, err := inspect(interfaceName, address)
	if err != nil || !present {
		return err
	}
	err = c.runner.Run(
		ctx,
		ifconfigPath,
		interfaceName,
		"inet",
		address.Addr().String(),
		"-alias",
	)
	if err != nil {
		present, inspectErr := inspect(interfaceName, address)
		if inspectErr == nil && !present {
			return nil
		}
		return errors.Join(err, inspectErr)
	}
	return nil
}

type commandRouteConfigurator struct {
	runner commandRunner
	usesInterface func(context.Context, netip.Prefix, string) (bool, error)
}

func (c commandRouteConfigurator) Available() bool {
	return c.runner != nil && c.runner.Available(routePath)
}

func (c commandRouteConfigurator) Add(
	ctx context.Context,
	interfaceName string,
	prefix netip.Prefix,
) error {
	return c.change(ctx, "add", interfaceName, prefix)
}

func (c commandRouteConfigurator) Delete(
	ctx context.Context,
	interfaceName string,
	prefix netip.Prefix,
) error {
	inspect := c.usesInterface
	if inspect == nil {
		inspect = systemRecoveryInspector{}.RouteUsesInterface
	}
	present, err := inspect(ctx, prefix, interfaceName)
	if err != nil || !present {
		return err
	}
	err = c.change(ctx, "delete", interfaceName, prefix)
	if err != nil {
		present, inspectErr := inspect(ctx, prefix, interfaceName)
		if inspectErr == nil && !present {
			return nil
		}
		return errors.Join(err, inspectErr)
	}
	return nil
}

func interfaceHasAddress(name string, address netip.Prefix) (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if iface.Name != name {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return false, err
		}
		for _, candidate := range addresses {
			prefix, err := netip.ParsePrefix(candidate.String())
			if err == nil && prefix.Addr() == address.Addr() {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c commandRouteConfigurator) change(
	ctx context.Context,
	operation string,
	interfaceName string,
	prefix netip.Prefix,
) error {
	arguments := []string{"-n", operation}
	if prefix.Addr().Is6() {
		arguments = append(arguments, "-inet6")
	}
	arguments = append(
		arguments,
		"-net",
		prefix.Masked().String(),
		"-interface",
		interfaceName,
	)
	return c.runner.Run(ctx, routePath, arguments...)
}
