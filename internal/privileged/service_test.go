package privileged

import (
	"context"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestClientServiceAppliesClientPlan(t *testing.T) {
	engine := &fakeClientEngine{
		fakeLifecycleEngine: fakeLifecycleEngine{available: true},
	}
	service := NewClientService(engine)
	request, err := ipc.NewRequest(
		"apply",
		MethodApply,
		model.ClientPlan{Mode: "relay"},
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("apply failed: %+v", response.Error)
	}
	if engine.plan.Mode != "relay" {
		t.Fatalf("client plan = %+v", engine.plan)
	}
	var status model.ClientPrivilegedStatus
	if err := model.DecodeStrict(response.Result, &status); err != nil {
		t.Fatalf("decode client status: %v", err)
	}
	if !status.Active {
		t.Fatalf("client status = %+v", status)
	}
}

func TestServerServiceAppliesServerPlan(t *testing.T) {
	engine := &fakeServerEngine{
		fakeLifecycleEngine: fakeLifecycleEngine{available: true},
	}
	service := NewServerService(engine)
	request, err := ipc.NewRequest(
		"apply",
		MethodApply,
		model.ServerPlan{ListenPort: 51820},
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("apply failed: %+v", response.Error)
	}
	if engine.plan.ListenPort != 51820 {
		t.Fatalf("server plan = %+v", engine.plan)
	}
	var status model.ServerPrivilegedStatus
	if err := model.DecodeStrict(response.Result, &status); err != nil {
		t.Fatalf("decode server status: %v", err)
	}
	if !status.Active {
		t.Fatalf("server status = %+v", status)
	}
}

func TestServiceReturnsRoleSpecificStatusEnvelope(t *testing.T) {
	clientService := NewClientService(&fakeClientEngine{
		fakeLifecycleEngine: fakeLifecycleEngine{available: true},
		status:              model.ClientPrivilegedStatus{Active: true},
	})
	request, err := ipc.NewRequest("client-status", MethodStatus, nil)
	if err != nil {
		t.Fatalf("new client status request: %v", err)
	}
	response := clientService.Handle(context.Background(), request)
	var clientStatus ClientStatus
	if err := model.DecodeStrict(response.Result, &clientStatus); err != nil {
		t.Fatalf("decode client status: %v", err)
	}
	if clientStatus.Role != "client" || !clientStatus.Network.Active {
		t.Fatalf("client status = %+v", clientStatus)
	}

	serverService := NewServerService(&fakeServerEngine{
		fakeLifecycleEngine: fakeLifecycleEngine{available: true},
		status: model.ServerPrivilegedStatus{
			Active:     true,
			PublicKey:  "server-key",
			ListenPort: 51820,
		},
	})
	request, err = ipc.NewRequest("server-status", MethodStatus, nil)
	if err != nil {
		t.Fatalf("new server status request: %v", err)
	}
	response = serverService.Handle(context.Background(), request)
	var serverStatus ServerStatus
	if err := model.DecodeStrict(response.Result, &serverStatus); err != nil {
		t.Fatalf("decode server status: %v", err)
	}
	if serverStatus.Role != "server" ||
		serverStatus.Network.PublicKey != "server-key" ||
		serverStatus.Network.ListenPort != 51820 {
		t.Fatalf("server status = %+v", serverStatus)
	}
}

func TestServiceRejectsWrongPlanShape(t *testing.T) {
	service := NewClientService(&fakeClientEngine{
		fakeLifecycleEngine: fakeLifecycleEngine{available: true},
	})
	request, err := ipc.NewRequest("apply", MethodApply, map[string]any{
		"unexpected": true,
		"peers":      []any{},
	})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if response.OK ||
		response.Error == nil ||
		response.Error.Code != model.ErrorInvalidArgument {
		t.Fatalf("response = %+v", response)
	}
}

type fakeLifecycleEngine struct {
	available bool
}

func (e *fakeLifecycleEngine) Available() bool {
	return e.available
}

func (*fakeLifecycleEngine) Recover(context.Context) error {
	return nil
}

func (*fakeLifecycleEngine) Remove(context.Context) error {
	return nil
}

type fakeClientEngine struct {
	fakeLifecycleEngine
	plan   model.ClientPlan
	status model.ClientPrivilegedStatus
}

func (e *fakeClientEngine) Status() model.ClientPrivilegedStatus {
	return e.status
}

func (e *fakeClientEngine) Apply(_ context.Context, plan model.ClientPlan) error {
	e.plan = plan
	e.status.Active = true
	return nil
}

type fakeServerEngine struct {
	fakeLifecycleEngine
	plan   model.ServerPlan
	status model.ServerPrivilegedStatus
}

func (e *fakeServerEngine) Status() model.ServerPrivilegedStatus {
	return e.status
}

func (e *fakeServerEngine) Apply(_ context.Context, plan model.ServerPlan) error {
	e.plan = plan
	e.status.Active = true
	return nil
}
