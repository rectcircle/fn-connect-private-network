package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/platform"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	"github.com/rectcircle/fn-connect-private-network/internal/server"
)

type Environment struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

func Run(ctx context.Context, arguments []string, environment Environment) int {
	if environment.Stdin == nil {
		environment.Stdin = os.Stdin
	}
	if environment.Stdout == nil {
		environment.Stdout = os.Stdout
	}
	if environment.Stderr == nil {
		environment.Stderr = os.Stderr
	}
	if len(arguments) == 0 {
		printUsage(environment.Stderr)
		return 2
	}

	var err error
	switch arguments[0] {
	case "version":
		_, err = fmt.Fprintln(environment.Stdout, environment.Version)
	case "status", "connect", "disconnect", "retry",
		"authorize", "logout", "forget", "diagnose":
		err = runClientCommand(ctx, arguments, environment)
	case "client":
		err = runClientProcess(ctx, arguments[1:], environment)
	case "server":
		err = runServerProcess(ctx, arguments[1:], environment)
	case "internal-healthcheck":
		err = runHealthcheck(ctx, arguments[1:])
	case "internal-cleanup":
		err = runCleanup(ctx, arguments[1:], environment)
	case "internal-purge-user":
		err = runPurgeUser(arguments[1:], environment)
	default:
		printUsage(environment.Stderr)
		return 2
	}
	if err == nil {
		return 0
	}

	typed := model.AsError(err)
	_, _ = fmt.Fprintf(environment.Stderr, "fncpn: %s\n", model.FormatError(err))
	switch typed.Code {
	case model.ErrorInvalidArgument:
		return 2
	case model.ErrorAuthRequired:
		return 3
	case model.ErrorPermissionDenied:
		return 6
	case model.ErrorNotFound, model.ErrorDeviceRevoked:
		return 7
	case model.ErrorAlreadyExists, model.ErrorConflict:
		return 5
	case model.ErrorFailedPrecondition, model.ErrorProtocol:
		return 8
	case model.ErrorTimeout:
		return 124
	case model.ErrorCanceled:
		return 130
	case model.ErrorUnavailable, model.ErrorResourceExhausted, model.ErrorDiscoveryFailed:
		return 4
	default:
		return 1
	}
}

func runPurgeUser(arguments []string, environment Environment) error {
	flags := flag.NewFlagSet("internal-purge-user", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	dataDirectory := flags.String(
		"data-dir",
		defaultClientDataDirectory(),
		"client data directory",
	)
	configPath := flags.String("config", defaultClientConfig(), "client configuration path")
	logDirectory := flags.String(
		"log-dir",
		filepath.Dir(defaultClientLog()),
		"client log directory",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	// The root uninstall script removes this user's credential directory.
	// This command runs as the user and never opens the old Keychain.
	if err := client.NewConfigStore(*configPath, nil).Clear(); err != nil {
		return err
	}
	if err := os.RemoveAll(*dataDirectory); err != nil {
		return fmt.Errorf("remove client data: %w", err)
	}
	if err := os.RemoveAll(*logDirectory); err != nil {
		return fmt.Errorf("remove client logs: %w", err)
	}
	return nil
}

func runHealthcheck(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("internal-healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	role := flags.String("role", "", "daemon role")
	socket := flags.String("socket", "", "daemon socket")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	switch *role {
	case "client":
		var status model.ClientStatus
		return (ipc.Client{SocketPath: *socket, Timeout: 5 * time.Second}).
			Call(ctx, client.MethodStatus, nil, &status)
	case "client-privileged":
		var status privileged.ClientStatus
		return (ipc.Client{SocketPath: *socket, Timeout: 5 * time.Second}).
			Call(ctx, privileged.MethodStatus, nil, &status)
	case "server-privileged":
		var status privileged.ServerStatus
		return (ipc.Client{SocketPath: *socket, Timeout: 5 * time.Second}).
			Call(ctx, privileged.MethodStatus, nil, &status)
	case "server":
		transport := &http.Transport{
			DialContext: func(
				dialContext context.Context,
				_, _ string,
			) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(dialContext, "unix", *socket)
			},
		}
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"http://fncpn/api/v1/bootstrap",
			nil,
		)
		if err != nil {
			return err
		}
		request.Header.Set("X-Trim-Userid", "healthcheck")
		response, err := (&http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		}).Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return client.HTTPResponseError(response, "healthcheck.server", nil)
		}
		return nil
	default:
		return model.NewError(model.ErrorInvalidArgument, "invalid healthcheck role", false)
	}
}

func runCleanup(
	ctx context.Context,
	arguments []string,
	environment Environment,
) error {
	if os.Geteuid() != 0 {
		return model.NewError(
			model.ErrorPermissionDenied,
			"cleanup must run as root",
			false,
		)
	}
	flags := flag.NewFlagSet("internal-cleanup", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	role := flags.String("role", "", "network role")
	stateDirectory := flags.String("state-dir", "", "privileged state directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	processLogger := logger(environment.Stderr)
	switch *role {
	case "client":
		engine := platform.NewClientEngine(processLogger, *stateDirectory)
		if err := engine.Recover(ctx); err != nil {
			return err
		}
		return engine.Remove(ctx)
	case "server":
		engine := platform.NewServerEngine(processLogger, *stateDirectory)
		if err := engine.Recover(ctx); err != nil {
			return err
		}
		return engine.Remove(ctx)
	default:
		return model.NewError(model.ErrorInvalidArgument, "invalid cleanup role", false)
	}
}

func runClientCommand(
	ctx context.Context,
	arguments []string,
	environment Environment,
) error {
	flags := flag.NewFlagSet(arguments[0], flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	socket := flags.String("socket", defaultClientSocket(), "client daemon socket")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	confirmed := flags.Bool("yes", false, "confirm destructive operation")
	if err := flags.Parse(arguments[1:]); err != nil {
		return model.WrapError(model.ErrorInvalidArgument, err.Error(), false, err)
	}
	ipcClient := ipc.Client{SocketPath: *socket}
	if arguments[0] == "authorize" {
		if flags.NArg() != 1 {
			return model.NewError(
				model.ErrorInvalidArgument,
				"authorize requires one FN ID",
				false,
			)
		}
		fnID, err := client.NormalizeFNID(flags.Arg(0))
		if err != nil {
			return model.WrapError(
				model.ErrorInvalidArgument,
				err.Error(),
				false,
				err,
			)
		}
		var pending client.AuthorizationResult
		if err := ipcClient.Call(
			ctx,
			client.MethodAuthorizationBegin,
			client.AuthorizationRequest{FNID: fnID},
			&pending,
		); err != nil {
			return err
		}
		authorizationFinished := false
		defer func() {
			if authorizationFinished {
				return
			}
			cancelContext, cancel := context.WithTimeout(
				context.Background(),
				2*time.Second,
			)
			defer cancel()
			cancelClient := ipc.Client{
				SocketPath: *socket,
				Timeout:    2 * time.Second,
			}
			_ = cancelClient.Call(
				cancelContext,
				client.MethodAuthorizationCancel,
				client.AuthorizationRequest{RequestID: pending.RequestID},
				nil,
			)
		}()
		if err := platform.OpenAuthorization(ctx, fnID, pending.RequestID); err != nil {
			return err
		}
		waitContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		waitClient := ipcClient
		waitClient.Timeout = 5*time.Minute + 5*time.Second
		var result client.AuthorizationResult
		if err := waitClient.Call(
			waitContext,
			client.MethodAuthorizationWait,
			client.AuthorizationRequest{RequestID: pending.RequestID},
			&result,
		); err != nil {
			return err
		}
		authorizationFinished = true
		switch result.State {
		case "succeeded":
			_, err := fmt.Fprintln(environment.Stdout, "authorized")
			return err
		case "failed", "canceled":
			if result.Error != nil {
				return result.Error
			}
		}
		return model.NewError(
			model.ErrorInternal,
			"authorization did not complete",
			false,
		)
	}
	if flags.NArg() != 0 {
		return model.NewError(model.ErrorInvalidArgument, "unexpected arguments", false)
	}
	if arguments[0] == "forget" && !*confirmed {
		return model.NewError(
			model.ErrorInvalidArgument,
			"forget requires --yes",
			false,
		)
	}

	if arguments[0] == "diagnose" {
		var diagnostics model.ClientDiagnostics
		if err := ipcClient.Call(ctx, arguments[0], nil, &diagnostics); err != nil {
			return err
		}
		return writeJSON(environment.Stdout, diagnostics)
	}
	var status model.ClientStatus
	if err := ipcClient.Call(ctx, arguments[0], nil, &status); err != nil {
		return err
	}
	if *jsonOutput || arguments[0] == "status" {
		return writeJSON(environment.Stdout, status)
	}
	_, err := fmt.Fprintln(environment.Stdout, status.State)
	return err
}

func runClientProcess(
	ctx context.Context,
	arguments []string,
	environment Environment,
) (resultErr error) {
	if len(arguments) == 0 {
		return model.NewError(model.ErrorInvalidArgument, "client subcommand is required", false)
	}
	switch arguments[0] {
	case "daemon":
		flags := flag.NewFlagSet("client daemon", flag.ContinueOnError)
		flags.SetOutput(environment.Stderr)
		socket := flags.String("socket", defaultClientSocket(), "client daemon socket")
		privilegedSocket := flags.String(
			"privileged-socket",
			defaultPrivilegedSocket("client"),
			"client privileged daemon socket",
		)
		configPath := flags.String(
			"config",
			defaultClientConfig(),
			"client configuration path",
		)
		logPath := flags.String("log-file", defaultClientLog(), "client daemon log file")
		if err := flags.Parse(arguments[1:]); err != nil {
			return model.WrapError(model.ErrorInvalidArgument, err.Error(), false, err)
		}
		if flags.NArg() != 0 {
			return model.NewError(model.ErrorInvalidArgument, "unexpected arguments", false)
		}
		processLogger, logWriter, err := openProcessLogger(*logPath, environment.Stderr)
		if err != nil {
			return err
		}
		defer logWriter.Close()
		processLogger = processLogger.With("role", "client", "version", environment.Version)
		processLogger.Info("client daemon starting", "socket", *socket)
		defer func() {
			if resultErr != nil {
				processLogger.Error("client daemon failed", logging.ErrorAttrs(model.WithOperation(resultErr, "client.lifecycle"))...)
			} else {
				processLogger.Info("client daemon stopped")
			}
		}()
		runContext, stop := context.WithCancel(ctx)
		defer stop()
		store := client.NewConfigStore(*configPath, client.IPCSecretStore{
			Context: runContext,
			Client:  ipc.Client{SocketPath: *privilegedSocket},
		})
		manager, err := client.NewManager(client.ManagerOptions{
			Store:      store,
			Discoverer: client.DiscoveryClient{},
			Remote: func(
				fnID string,
				cookies []client.Cookie,
				onCookies func([]client.Cookie) error,
			) (client.RemoteService, error) {
				baseURL, err := client.DefaultRemoteURL(fnID)
				if err != nil {
					return nil, err
				}
				return client.NewRemoteClient(
					baseURL,
					cookies,
					nil,
					onCookies,
				)
			},
			Privileged: client.IPCPrivilegedNetwork{Client: ipc.Client{
				SocketPath: *privilegedSocket,
			}},
			Bridge:  &client.RelayBridge{Logger: processLogger},
			Probe:   client.SystemLocalProbe{},
			Monitor: platform.NewNetworkMonitor(),
			Logger:  processLogger,
		})
		if err != nil {
			return err
		}
		if err := manager.Start(); err != nil {
			return fmt.Errorf("start client manager: %w", err)
		}
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			manager.Run(runContext)
		}()
		clientService := client.NewWithRuntime(manager)
		clientService.Logger = processLogger
		serveErr := ipc.Server{
			SocketPath: *socket,
			Mode:       0o600,
			Handler:    clientService,
			Logger:     processLogger,
		}.Serve(runContext)
		stop()
		<-runDone
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result := errors.Join(serveErr, manager.Close(cleanupContext))
		if result != nil {
			typed := model.AsError(result)
			processLogger.Error(
				"client daemon stopped with an error",
				"code",
				typed.Code,
				"cause",
				logging.Redact(result.Error()),
			)
		}
		return result
	case "privileged-daemon":
		return runPrivilegedProcess(ctx, "client", arguments[1:], environment)
	default:
		return model.NewError(
			model.ErrorInvalidArgument,
			"unsupported client subcommand",
			false,
		)
	}
}

func runServerProcess(
	ctx context.Context,
	arguments []string,
	environment Environment,
) error {
	if len(arguments) == 0 {
		return model.NewError(model.ErrorInvalidArgument, "server subcommand is required", false)
	}
	switch arguments[0] {
	case "daemon":
		return runServerDaemon(ctx, arguments[1:], environment)
	case "privileged-daemon":
		return runPrivilegedProcess(ctx, "server", arguments[1:], environment)
	default:
		return model.NewError(
			model.ErrorInvalidArgument,
			"unsupported server subcommand",
			false,
		)
	}
}

func runPrivilegedProcess(
	ctx context.Context,
	role string,
	arguments []string,
	environment Environment,
) (resultErr error) {
	if os.Geteuid() != 0 {
		return model.NewError(
			model.ErrorPermissionDenied,
			"privileged-daemon must run as root",
			false,
		)
	}
	flags := flag.NewFlagSet(role+" privileged-daemon", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	socket := flags.String(
		"socket",
		defaultPrivilegedSocket(role),
		"privileged daemon socket",
	)
	allowedUID := flags.Int(
		"allow-uid",
		-1,
		"non-root UID allowed to call the privileged daemon",
	)
	stateDirectory := flags.String(
		"state-dir",
		defaultPrivilegedStateDirectory(role),
		"privileged network state directory",
	)
	logPath := flags.String(
		"log-file",
		defaultPrivilegedLog(role),
		"privileged daemon log file",
	)
	if err := flags.Parse(arguments); err != nil {
		return model.WrapError(model.ErrorInvalidArgument, err.Error(), false, err)
	}
	if flags.NArg() != 0 {
		return model.NewError(model.ErrorInvalidArgument, "unexpected arguments", false)
	}
	processLogger, logWriter, err := openProcessLogger(*logPath, environment.Stderr)
	if err != nil {
		return err
	}
	defer logWriter.Close()
	processLogger = processLogger.With("role", role+"-privileged", "version", environment.Version)
	processLogger.Info("privileged daemon starting", "socket", *socket)
	defer func() {
		if resultErr != nil {
			processLogger.Error("privileged daemon failed", logging.ErrorAttrs(model.WithOperation(resultErr, role+".privileged.lifecycle"))...)
		} else {
			processLogger.Info("privileged daemon stopped")
		}
	}()
	var service interface {
		ipc.Handler
		Recover(context.Context) error
		Close(context.Context) error
	}
	switch role {
	case "client":
		secrets, err := privileged.OpenSecretStore(
			filepath.Join(*stateDirectory, "credentials"), processLogger,
		)
		if err != nil {
			processLogger.Error("open client credential storage", "error", err)
			return fmt.Errorf("open client credential storage: %w", err)
		}
		service = privileged.NewClientService(
			platform.NewClientEngine(processLogger, *stateDirectory),
			secrets,
		)
	case "server":
		service = privileged.NewServerService(
			platform.NewServerEngine(processLogger, *stateDirectory),
		)
	default:
		return model.NewError(
			model.ErrorInvalidArgument,
			"unsupported privileged daemon role",
			false,
		)
	}
	if err := service.Recover(ctx); err != nil {
		return fmt.Errorf("recover privileged network state: %w", err)
	}
	processLogger.Info("privileged network recovery completed")
	serveErr := ipc.Server{
		SocketPath: *socket,
		Mode:       0o666,
		Authorize:  privileged.AuthorizePeer(role, *allowedUID),
		Handler:    service,
		Logger:     processLogger,
	}.Serve(ctx)
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanupErr := service.Close(cleanupContext)
	if cleanupErr != nil {
		processLogger.Error(
			"clean up privileged network state",
			"role",
			role,
			"error",
			cleanupErr,
		)
	}
	result := errors.Join(serveErr, cleanupErr)
	if result != nil {
		typed := model.AsError(result)
		processLogger.Error(
			"privileged daemon stopped with an error",
			"role",
			role,
			"code",
			typed.Code,
			"cause",
			logging.Redact(result.Error()),
		)
	}
	return result
}

func runServerDaemon(
	ctx context.Context,
	arguments []string,
	environment Environment,
) (resultErr error) {
	flags := flag.NewFlagSet("server daemon", flag.ContinueOnError)
	flags.SetOutput(environment.Stderr)
	socketPath := flags.String("socket", "", "fnOS gateway Unix socket")
	stateDirectory := flags.String("state-dir", "", "server state directory")
	privilegedSocket := flags.String(
		"privileged-socket",
		defaultPrivilegedSocket("server"),
		"server privileged daemon socket",
	)
	logPath := flags.String("log-file", "", "server daemon log file")
	if err := flags.Parse(arguments); err != nil {
		return model.WrapError(model.ErrorInvalidArgument, err.Error(), false, err)
	}
	if flags.NArg() != 0 {
		return model.NewError(model.ErrorInvalidArgument, "unexpected arguments", false)
	}
	if *socketPath == "" {
		return model.NewError(
			model.ErrorInvalidArgument,
			"--socket is required",
			false,
		)
	}
	if *stateDirectory == "" {
		return model.NewError(
			model.ErrorInvalidArgument,
			"--state-dir is required",
			false,
		)
	}
	processLogger := logger(environment.Stderr)
	var logWriter io.Closer
	var err error
	if *logPath != "" {
		processLogger, logWriter, err = openProcessLogger(*logPath, environment.Stderr)
		if err != nil {
			return err
		}
		defer logWriter.Close()
	}
	processLogger = processLogger.With("role", "server", "version", environment.Version)
	processLogger.Info("server daemon starting", "socket", *socketPath)
	defer func() {
		if resultErr != nil {
			processLogger.Error("server daemon failed", logging.ErrorAttrs(model.WithOperation(resultErr, "server.lifecycle"))...)
		} else {
			processLogger.Info("server daemon stopped")
		}
	}()
	network := server.IPCNetwork{Client: ipc.Client{
		SocketPath: *privilegedSocket,
		Timeout:    30 * time.Second,
	}}
	store, err := server.OpenService(*stateDirectory, network, processLogger)
	if err != nil {
		typed := model.AsError(err)
		processLogger.Error(
			"open server state",
			"component",
			"server",
			"phase",
			"open_state",
			"code",
			typed.Code,
			"cause",
			logging.Redact(err.Error()),
		)
		return fmt.Errorf("open server state: %w", err)
	}
	initial := store.Snapshot()
	reconciled := false
	if len(initial.Devices) == 0 &&
		len(initial.Settings.LANCIDRs) == 0 {
		lanCIDRs, detectErr := platform.DetectLANCIDRs()
		if detectErr != nil {
			processLogger.Error(
				"detect primary LAN network",
				"error",
				detectErr,
			)
		} else if len(lanCIDRs) > 0 {
			if _, err := store.UpdateNetworks(ctx, server.NetworkUpdate{
				LANCIDRs: &lanCIDRs,
			}); err != nil {
				processLogger.Error(
					"apply detected LAN network",
					"error",
					err,
				)
			} else {
				reconciled = true
			}
		}
	}
	if !reconciled {
		if err := store.Reconcile(ctx); err != nil {
			processLogger.Error("reconcile server network", "error", err)
		}
	}
	localProbe, err := server.OpenLocalProbeService(
		*stateDirectory, store, server.DefaultLocalProbePort,
	)
	if err != nil {
		return fmt.Errorf("initialize local probe: %w", err)
	}
	defer localProbe.Close()
	probeEvents, monitorErr := platform.LANAddressEvents(ctx)
	if monitorErr != nil {
		processLogger.Error("monitor local probe addresses", "error", monitorErr)
	}
	go localProbe.Run(ctx, platform.DetectLANAddresses, probeEvents, processLogger)
	listener, cleanup, err := openHTTPListener(*socketPath)
	if err != nil {
		return err
	}
	defer cleanup()
	processLogger.Info("server HTTP listener ready", "socket", *socketPath)

	handler := server.NewHTTPHandler(store, localProbe)
	go handler.Run(ctx)
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(processLogger.Handler(), slog.LevelError),
	}
	errs := make(chan error, 1)
	go func() {
		errs <- httpServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errs:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		processLogger.Error(
			"server daemon stopped with an error",
			"code",
			model.AsError(err).Code,
			"cause",
			logging.Redact(err.Error()),
		)
		return err
	}
}

func openHTTPListener(socketPath string) (net.Listener, func(), error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return nil, func() {}, err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, func() {}, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, func() {}, err
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		_ = listener.Close()
		return nil, func() {}, err
	}
	return listener, func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}, nil
}

func defaultClientSocket() string {
	return filepath.Join(defaultClientDataDirectory(), "run", "client.sock")
}

func defaultPrivilegedSocket(role string) string {
	return filepath.Join("/var/run", "fncpn-"+role+"-privileged.sock")
}

func defaultPrivilegedStateDirectory(role string) string {
	if role == "server" {
		return "/var/lib/fncpn"
	}
	return "/var/db/fncpn"
}

func defaultClientConfig() string {
	return filepath.Join(defaultClientDataDirectory(), "config.json")
}

func defaultClientDataDirectory() string {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "FnCPN")
	}
	return filepath.Join(configDirectory, "FnCPN")
}

func defaultClientLog() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "fncpn-client.log")
	}
	return filepath.Join(home, "Library", "Logs", "FnCPN", "client.log")
}

func defaultPrivilegedLog(role string) string {
	if role == "client" {
		return "/var/log/fncpn/client-privileged.log"
	}
	return "/var/log/fncpn/server-privileged.log"
}

func openProcessLogger(
	path string,
	fallback io.Writer,
) (*slog.Logger, io.Closer, error) {
	writer, err := logging.Open(
		path,
		logging.DefaultMaxSize,
		logging.DefaultBackups,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("open process log: %w", err)
	}
	return logger(logging.MultiWriter(writer, fallback)), writer, nil
}

func logger(writer io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: logging.RedactAttr,
	}))
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, `usage:
  fncpn version
  fncpn status [--json]
  fncpn authorize <fn-id>
  fncpn connect
  fncpn disconnect
  fncpn retry
  fncpn logout
  fncpn forget --yes
  fncpn diagnose [--json]
  fncpn client daemon [--socket PATH] [--privileged-socket PATH] [--config PATH]
  fncpn client privileged-daemon [--socket PATH] [--state-dir PATH]
  fncpn server daemon --socket PATH --state-dir PATH [--privileged-socket PATH]
  fncpn server privileged-daemon [--socket PATH] [--state-dir PATH] [--allow-uid UID]`)
}

func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
