package server

import (
	"context"
	"errors"
	"net/netip"
	"slices"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

type IPCNetwork struct {
	Client ipc.Client
}

func (c IPCNetwork) Apply(
	ctx context.Context,
	state model.ServerState,
) (model.ServerPrivilegedStatus, error) {
	plan, err := ServerPlan(state)
	if err != nil {
		return model.ServerPrivilegedStatus{}, err
	}
	var status model.ServerPrivilegedStatus
	err = c.Client.Call(ctx, privileged.MethodApply, plan, &status)
	if err == nil && status.LastError != nil {
		err = status.LastError
	}
	return status, err
}

func (c IPCNetwork) Status(
	ctx context.Context,
) (model.ServerPrivilegedStatus, error) {
	var response privileged.ServerStatus
	if err := c.Client.Call(ctx, privileged.MethodStatus, nil, &response); err != nil {
		return model.ServerPrivilegedStatus{}, err
	}
	if !response.Available {
		err := model.NewError(
			model.ErrorUnavailable,
			"server privileged network engine is unavailable",
			true,
		)
		return model.ServerPrivilegedStatus{}, err
	}
	if response.Network.LastError != nil {
		return response.Network, response.Network.LastError
	}
	return response.Network, nil
}

func cloneServerNetworkSnapshot(
	source model.ServerNetworkSnapshot,
) model.ServerNetworkSnapshot {
	snapshot := source
	snapshot.Network.Peers = slices.Clone(source.Network.Peers)
	for index := range snapshot.Network.Peers {
		if snapshot.Network.Peers[index].LastHandshake != nil {
			value := *snapshot.Network.Peers[index].LastHandshake
			snapshot.Network.Peers[index].LastHandshake = &value
		}
	}
	if source.LastError != nil {
		copied := *source.LastError
		snapshot.LastError = &copied
	}
	return snapshot
}

func ServerPlan(state model.ServerState) (model.ServerPlan, error) {
	if err := validateState(state); err != nil {
		return model.ServerPlan{}, err
	}
	serverAddress, err := ServerAddress(state.Settings.OverlayCIDR)
	if err != nil {
		return model.ServerPlan{}, err
	}
	peers := make([]model.Peer, 0, len(state.Devices))
	for _, device := range state.Devices {
		if !device.Enabled {
			continue
		}
		peers = append(peers, model.Peer{
			PublicKey: device.PublicKey,
			Address:   device.OverlayAddress,
		})
	}
	return model.ServerPlan{
		OverlayCIDR:   state.Settings.OverlayCIDR,
		ServerAddress: serverAddress,
		ListenPort:    state.Settings.ListenPort,
		LANCIDRs:      slices.Clone(state.Settings.LANCIDRs),
		Peers:         peers,
	}, nil
}

func ClientConfiguration(
	state model.ServerState,
	device model.Device,
	status model.ServerPrivilegedStatus,
) (model.ClientConfiguration, error) {
	if !device.Enabled {
		return model.ClientConfiguration{}, model.NewError(
			model.ErrorDeviceRevoked,
			"device is disabled",
			false,
		)
	}
	if !status.Active || status.Degraded || status.PublicKey == "" {
		return model.ClientConfiguration{}, model.NewError(
			model.ErrorUnavailable,
			"server network is unavailable",
			true,
		)
	}
	serverAddress, err := ServerAddress(state.Settings.OverlayCIDR)
	if err != nil {
		return model.ClientConfiguration{}, err
	}
	serverPrefix, _ := netip.ParsePrefix(serverAddress)
	allowedIPs := []string{
		netip.PrefixFrom(serverPrefix.Addr(), 32).String(),
	}
	allowedIPs = append(allowedIPs, state.Settings.LANCIDRs...)
	return wgconfig.NormalizeClientConfiguration(model.ClientConfiguration{
		DeviceID:        device.ID,
		ServerPublicKey: status.PublicKey,
		ServerAddress:   serverAddress,
		ClientAddress:   device.OverlayAddress,
		ListenPort:      state.Settings.ListenPort,
		AllowedIPs:      allowedIPs,
	})
}

func FindDevice(state model.ServerState, id string) (model.Device, error) {
	for _, device := range state.Devices {
		if device.ID == id {
			return device, nil
		}
	}
	return model.Device{}, errors.New("device not found")
}

func applyPeerStatus(
	devices []model.Device,
	peers []model.PrivilegedPeerStatus,
) {
	byKey := make(map[string]model.PrivilegedPeerStatus, len(peers))
	for _, peer := range peers {
		byKey[peer.PublicKey] = peer
	}
	for index := range devices {
		peer, ok := byKey[devices[index].PublicKey]
		if !ok {
			continue
		}
		devices[index].LastHandshake = peer.LastHandshake
		devices[index].ReceiveBytes = peer.ReceiveBytes
		devices[index].TransmitBytes = peer.TransmitBytes
	}
}
