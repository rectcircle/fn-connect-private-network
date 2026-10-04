package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	MethodGetSecret     = "get-client-secret"
	MethodPutSecret     = "put-client-secret"
	MethodDeleteSecret  = "delete-client-secret"
	WireGuardSecret     = "com.rectcircle.fncpn.wireguard"
	CookieSecret        = "com.rectcircle.fncpn.cookies"
	NativeSessionSecret = "com.rectcircle.fncpn.native-session"
	maxSecretSize       = 64 * 1024
)

// UID is deliberately absent: the server derives it from the Unix peer.
type SecretRequest struct {
	Service string `json:"service"`
	Account string `json:"account"`
	Value   []byte `json:"value,omitempty"`
}

type SecretResult struct {
	Found bool   `json:"found"`
	Value []byte `json:"value,omitempty"`
}

type SecretStore struct {
	mu        sync.Mutex
	directory string
	logger    *slog.Logger
}

func OpenSecretStore(directory string, logger *slog.Logger) (*SecretStore, error) {
	if err := privateSecretDirectory(filepath.Dir(directory)); err != nil {
		return nil, err
	}
	if err := privateSecretDirectory(directory); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SecretStore{directory: directory, logger: logger}, nil
}

func (s *SecretStore) Handle(ctx context.Context, request ipc.Request) ipc.Response {
	peer, ok := ipc.PeerIdentityFromContext(ctx)
	if !ok || peer.PID <= 0 {
		return ipc.Failure(request.ID, ipc.RejectPeer("credential caller identity is required"))
	}
	input, err := ipc.DecodeParams[SecretRequest](request)
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	if (input.Service != WireGuardSecret &&
		input.Service != CookieSecret &&
		input.Service != NativeSessionSecret) ||
		len(input.Account) == 0 || len(input.Account) > 63 ||
		len(input.Value) > maxSecretSize ||
		(request.Method == MethodPutSecret && len(input.Value) == 0) ||
		(request.Method != MethodPutSecret && len(input.Value) != 0) {
		return ipc.Failure(request.ID, model.NewError(
			model.ErrorInvalidArgument, "invalid credential parameters", false,
		))
	}
	if err := ctx.Err(); err != nil {
		return ipc.Failure(request.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.access(peer.UID, request.Method, input)
	if err != nil {
		s.logger.Error("access client credential", "method", request.Method, "uid", peer.UID, "error", err)
		return ipc.Failure(request.ID, model.WrapError(
			model.ErrorUnavailable, "client credential storage failed", true, err,
		))
	}
	return ipc.Success(request.ID, result)
}

func (s *SecretStore) access(uid uint32, method string, input SecretRequest) (SecretResult, error) {
	directory := filepath.Join(s.directory, strconv.FormatUint(uint64(uid), 10))
	// Opaque names keep untrusted account strings out of filesystem paths.
	digest := sha256.Sum256([]byte(input.Service + "\x00" + input.Account))
	path := filepath.Join(directory, hex.EncodeToString(digest[:])+".secret")
	if method == MethodPutSecret {
		if err := privateSecretDirectory(directory); err != nil {
			return SecretResult{}, err
		}
	} else if info, err := os.Lstat(directory); err != nil {
		if os.IsNotExist(err) {
			return SecretResult{}, nil
		}
		return SecretResult{}, err
	} else if err := checkSecretPermissions(info, true); err != nil {
		return SecretResult{}, err
	}

	info, err := os.Lstat(path)
	found := err == nil
	if err != nil && !os.IsNotExist(err) {
		return SecretResult{}, err
	}
	if found {
		if err := checkSecretPermissions(info, false); err != nil {
			return SecretResult{}, err
		}
	}
	switch method {
	case MethodGetSecret:
		if !found {
			return SecretResult{}, nil
		}
		file, err := os.Open(path)
		if err != nil {
			return SecretResult{}, err
		}
		defer file.Close()
		value, err := io.ReadAll(io.LimitReader(file, maxSecretSize+1))
		if err != nil {
			return SecretResult{}, err
		}
		if len(value) > maxSecretSize {
			return SecretResult{}, errors.New("credential file exceeds size limit")
		}
		return SecretResult{Found: true, Value: value}, nil
	case MethodPutSecret:
		if err := writeSecretAtomic(path, input.Value); err != nil {
			return SecretResult{}, err
		}
		if input.Service == WireGuardSecret {
			s.logger.Info("client WireGuard identity stored", "uid", uid,
				"fn_id", model.SafeText(input.Account), "replaced", found)
		}
		return SecretResult{}, nil
	case MethodDeleteSecret:
		if !found {
			return SecretResult{}, nil
		}
		if err := os.Remove(path); err != nil {
			return SecretResult{}, err
		}
		if err := syncSecretDirectory(directory); err != nil {
			return SecretResult{}, err
		}
		if input.Service == WireGuardSecret {
			s.logger.Info("client WireGuard identity removed", "uid", uid, "fn_id", model.SafeText(input.Account))
		}
		return SecretResult{}, nil
	default:
		return SecretResult{}, errors.New("unsupported credential operation")
	}
}

func privateSecretDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return checkSecretPermissions(info, true)
}

func checkSecretPermissions(info os.FileInfo, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	// The production caller is the root-only privileged daemon. Tests use
	// private temporary directories owned by the test process instead.
	if !ok || stat.Uid != uint32(os.Geteuid()) ||
		info.Mode().Perm() != mode ||
		(directory && !info.IsDir()) ||
		(!directory && !info.Mode().IsRegular()) {
		return errors.New("credential storage permissions or owner are invalid")
	}
	return nil
}

func writeSecretAtomic(path string, value []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncSecretDirectory(directory)
}

func syncSecretDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
