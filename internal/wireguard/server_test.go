package wireguard

import (
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestNormalizeServerPlan(t *testing.T) {
	plan := validServerPlan()
	normalized, err := NormalizeServerPlan(plan)
	if err != nil {
		t.Fatalf("normalize server plan: %v", err)
	}
	if normalized.ServerAddress != "10.203.0.1/24" ||
		normalized.Peers[0].Address != "10.203.0.2/32" {
		t.Fatalf("normalized plan = %+v", normalized)
	}
}

func TestNormalizeServerPlanRejectsInvalidTopology(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.ServerPlan)
	}{
		{
			name: "server address",
			mutate: func(plan *model.ServerPlan) {
				plan.ServerAddress = "10.203.0.2/24"
			},
		},
		{
			name: "LAN overlap",
			mutate: func(plan *model.ServerPlan) {
				plan.LANCIDRs = []string{"10.203.0.0/25"}
			},
		},
		{
			name: "public LAN",
			mutate: func(plan *model.ServerPlan) {
				plan.LANCIDRs = []string{"8.8.8.0/24"}
			},
		},
		{
			name: "duplicate key",
			mutate: func(plan *model.ServerPlan) {
				plan.Peers = append(plan.Peers, model.Peer{
					PublicKey: plan.Peers[0].PublicKey,
					Address:   "10.203.0.3/32",
				})
			},
		},
		{
			name: "zero key",
			mutate: func(plan *model.ServerPlan) {
				plan.Peers[0].PublicKey = testKey(0)
			},
		},
		{
			name: "network address",
			mutate: func(plan *model.ServerPlan) {
				plan.Peers[0].Address = "10.203.0.0/32"
			},
		},
		{
			name: "duplicate address",
			mutate: func(plan *model.ServerPlan) {
				plan.Peers = append(plan.Peers, model.Peer{
					PublicKey: testKey(4),
					Address:   plan.Peers[0].Address,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validServerPlan()
			test.mutate(&plan)
			if _, err := NormalizeServerPlan(plan); err == nil {
				t.Fatal("invalid plan was accepted")
			}
		})
	}
}

func validServerPlan() model.ServerPlan {
	return model.ServerPlan{
		OverlayCIDR:   "10.203.0.0/24",
		ServerAddress: "10.203.0.1/24",
		ListenPort:    51820,
		LANCIDRs:      []string{"192.168.71.0/24"},
		Peers: []model.Peer{
			{PublicKey: testKey(3), Address: "10.203.0.2/32"},
		},
	}
}
