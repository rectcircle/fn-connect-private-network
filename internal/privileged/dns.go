package privileged

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
)

const MethodApplyDNS = "apply-dns"
const MethodRemoveDNS = "remove-dns"
const MethodRenewDNS = "renew-dns"

type dnsJournal struct {
	Version int           `json:"version"`
	Plan    model.DNSPlan `json:"plan"`
	Owner   peer.Identity `json:"owner"`
}
type DNSRules struct {
	authorize ipc.Authorizer
	install   func(model.DNSPlan) error
	remove    func(model.DNSPlan) error
	verify    func(model.DNSPlan) error
	mu        sync.Mutex
	path      string
	current   *dnsJournal
	expires   time.Time
}

func NewDNSRules(dir string) *DNSRules {
	return &DNSRules{authorize: AuthorizePeer("client", -1), path: filepath.Join(dir, "dns-state.json"), install: installDNSRule, remove: removeDNSRule, verify: verifyDNSRule}
}

// WithAuthorizer keeps lease renewal authorization aligned with per-UID Linux services.
func (r *DNSRules) WithAuthorizer(authorize ipc.Authorizer) *DNSRules {
	r.authorize = authorize
	return r
}
func validateDNSPlan(p model.DNSPlan) error {
	if err := dns.ValidateSuffix(p.Suffix); err != nil {
		return err
	}
	a, err := netip.ParseAddr(p.Address)
	if err != nil || !a.Is4() || !a.IsLoopback() {
		return errors.New("DNS listener must be IPv4 loopback")
	}
	if _, err := hex.DecodeString(p.Generation); err != nil {
		return errors.New("invalid DNS generation")
	}
	if p.Port == 0 || len(p.Generation) != 32 {
		return errors.New("invalid DNS listener port or generation")
	}
	return validatePlatformDNS(p)
}
func (r *DNSRules) Recover(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.Remove(r.path + ".tmp"); err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j dnsJournal
	if err = model.DecodeStrict(data, &j); err != nil {
		return err
	}
	if j.Version != 1 {
		return errors.New("unsupported DNS journal version")
	}
	if err = validateDNSPlan(j.Plan); err != nil {
		return err
	}
	r.current = &j
	return r.removeLocked()
}
func (r *DNSRules) removeLocked() error {
	if r.current == nil {
		return nil
	}
	if err := r.remove(r.current.Plan); err != nil {
		return err
	}
	if err := os.Remove(r.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	r.current = nil
	return nil
}
func (r *DNSRules) Close(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removeLocked()
}
func (r *DNSRules) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.mu.Lock()
			if r.current != nil && (time.Now().After(r.expires) || r.authorize(ctx, r.current.Owner) != nil) {
				_ = r.removeLocked()
			}
			r.mu.Unlock()
		}
	}
}
func sameDNSOwner(a, b peer.Identity) bool {
	return a.UID == b.UID && a.PID == b.PID && a.SID == b.SID && a.SessionID == b.SessionID
}
func (r *DNSRules) Handle(ctx context.Context, req ipc.Request) ipc.Response {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, ok := peer.IdentityFromContext(ctx)
	if !ok || owner.PID <= 0 {
		return ipc.Failure(req.ID, model.NewError(model.ErrorPermissionDenied, "DNS owner identity unavailable", false))
	}
	plan, err := ipc.DecodeParams[model.DNSPlan](req)
	if err != nil {
		return ipc.Failure(req.ID, err)
	}
	if req.Method == MethodRenewDNS {
		if r.current == nil || !sameDNSOwner(r.current.Owner, owner) || plan != r.current.Plan {
			return ipc.Failure(req.ID, model.NewError(model.ErrorFailedPrecondition, "DNS lease is no longer active", true))
		}
		r.expires = time.Now().Add(20 * time.Second)
		return ipc.Success(req.ID, nil)
	}
	if req.Method == MethodRemoveDNS {
		if r.current != nil && (!sameDNSOwner(r.current.Owner, owner) || plan.Generation != r.current.Plan.Generation) {
			return ipc.Failure(req.ID, model.NewError(model.ErrorPermissionDenied, "DNS rule belongs to another owner or generation", false))
		}
		if err = r.removeLocked(); err != nil {
			return ipc.Failure(req.ID, err)
		}
		return ipc.Success(req.ID, nil)
	}
	if err = validateDNSPlan(plan); err != nil {
		return ipc.Failure(req.ID, model.WrapError(model.ErrorInvalidArgument, "invalid DNS plan", false, err))
	}
	if r.current != nil {
		if !sameDNSOwner(r.current.Owner, owner) {
			return ipc.Failure(req.ID, model.NewError(model.ErrorConflict, "DNS is owned by another client", true))
		}
		if r.current.Plan == plan {
			if err := r.verify(plan); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return ipc.Failure(req.ID, err)
				}
				if err := r.install(plan); err != nil {
					return ipc.Failure(req.ID, err)
				}
			}
			r.expires = time.Now().Add(20 * time.Second)
			return ipc.Success(req.ID, nil)
		}
		if err = r.removeLocked(); err != nil {
			return ipc.Failure(req.ID, err)
		}
	}
	j := dnsJournal{Version: 1, Plan: plan, Owner: owner}
	if err = os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return ipc.Failure(req.ID, err)
	}
	data, _ := json.Marshal(j)
	tmp := r.path + ".tmp"
	file, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ipc.Failure(req.ID, e)
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err == nil {
		err = os.Rename(tmp, r.path)
		if err == nil && runtime.GOOS != "windows" {
			err = syncSecretDirectory(filepath.Dir(r.path))
		}
	}
	_ = os.Remove(tmp)
	if err != nil {
		return ipc.Failure(req.ID, err)
	}
	r.current = &j
	if err = r.install(plan); err != nil {
		var pending interface{ CleanupPending() bool }
		if errors.As(err, &pending) && pending.CleanupPending() {
			return ipc.Failure(req.ID, model.WrapError(model.ErrorUnavailable, "install DNS rule; cleanup pending", true, err))
		}
		cleanupErr := os.Remove(r.path)
		r.current = nil
		return ipc.Failure(req.ID, model.WrapError(model.ErrorUnavailable, fmt.Sprintf("install DNS rule: %v", err), true, cleanupErr))
	}
	r.expires = time.Now().Add(20 * time.Second)
	return ipc.Success(req.ID, nil)
}
