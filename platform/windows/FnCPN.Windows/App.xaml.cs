using System.Buffers.Binary;
using System.IO.Pipes;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text.Json;
using System.Windows;
using Microsoft.Win32.SafeHandles;

namespace FnCPN.Windows;

public partial class App : System.Windows.Application
{
    private static int CurrentSessionId => System.Diagnostics.Process.GetCurrentProcess().SessionId;
    private static string ActivationPipe => $"FnCPN.UI.Activation.{CurrentSessionId}";
    private Mutex? _instance;
    private bool _ownsInstance;
    private CancellationTokenSource? _activationLifetime;
    private const int MaximumActivationBytes = 8192;

    private async void OnStartup(object sender, StartupEventArgs e)
    {
        _instance = new Mutex(true, InstanceMutexName(WindowsIdentity.GetCurrent().User?.Value, CurrentSessionId), out var first);
        _ownsInstance = first;
        if (!first)
        {
            if (!await ForwardActivationAsync(e.Args))
            {
                System.Windows.MessageBox.Show(
                    AppStrings.Get("FnCPN 已在运行，但无法将请求转交给现有窗口。请检查应用状态后重试。"),
                    "FnCPN", MessageBoxButton.OK, MessageBoxImage.Error);
            }
            Shutdown();
            return;
        }

        var window = new MainWindow();
        MainWindow = window;
        window.PrepareStartup(e.Args);
        window.Show();
        _activationLifetime = new CancellationTokenSource();
        _ = ListenForActivationAsync(window, _activationLifetime.Token);
        window.HandleActivation(e.Args);
    }

    internal static string InstanceMutexName(string? userSid, int sessionId) =>
        $"Local\\FnCPN.UI.{userSid ?? "unknown"}.{sessionId}";

    internal static bool ActivationPeerSessionMatches(uint clientSessionId, uint serverSessionId) =>
        clientSessionId == serverSessionId;

    private static bool IsActivationPeerInSession(SafePipeHandle pipe, int expectedSessionId)
    {
        if (expectedSessionId < 0 || !GetNamedPipeClientProcessId(pipe, out var processId) || processId == 0)
            return false;
        return ProcessIdToSessionId(processId, out var clientSessionId) &&
               ActivationPeerSessionMatches(clientSessionId, (uint)expectedSessionId);
    }

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetNamedPipeClientProcessId(SafePipeHandle pipe, out uint clientProcessId);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool ProcessIdToSessionId(uint processId, out uint sessionId);

    private static async Task<bool> ForwardActivationAsync(string[] args)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(args);
        if (payload.Length is 0 or > MaximumActivationBytes) return false;
        var header = new byte[sizeof(uint)];
        BinaryPrimitives.WriteUInt32BigEndian(header, (uint)payload.Length);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        try
        {
            while (!timeout.IsCancellationRequested)
            {
                try
                {
                    await using var pipe = new NamedPipeClientStream(".", ActivationPipe, PipeDirection.Out, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
                    await pipe.ConnectAsync(timeout.Token);
                    await pipe.WriteAsync(header, timeout.Token);
                    await pipe.WriteAsync(payload, timeout.Token);
                    await pipe.FlushAsync(timeout.Token);
                    return true;
                }
                catch (IOException) when (!timeout.IsCancellationRequested)
                {
                    await Task.Delay(250, timeout.Token);
                }
            }
        }
        catch (OperationCanceledException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
        return false;
    }

    private static async Task ListenForActivationAsync(MainWindow window, CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                await using var pipe = new NamedPipeServerStream(ActivationPipe, PipeDirection.In, 1, PipeTransmissionMode.Byte,
                    PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
                await pipe.WaitForConnectionAsync(cancellationToken);
                if (!IsActivationPeerInSession(pipe.SafePipeHandle, CurrentSessionId)) continue;
                using var requestTimeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                requestTimeout.CancelAfter(TimeSpan.FromSeconds(5));
                var header = new byte[sizeof(uint)];
                await pipe.ReadExactlyAsync(header, requestTimeout.Token);
                var length = BinaryPrimitives.ReadUInt32BigEndian(header);
                if (length is 0 or > MaximumActivationBytes) continue;
                var payload = new byte[checked((int)length)];
                await pipe.ReadExactlyAsync(payload, requestTimeout.Token);
                var activationArgs = JsonSerializer.Deserialize<string[]>(payload);
                if (activationArgs is not null)
                {
                    window.Dispatcher.Invoke(() =>
                    {
                        if (activationArgs.Length == 0) window.ActivateFromExternalLaunch();
                        else window.HandleActivation(activationArgs);
                    });
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { return; }
            catch (OperationCanceledException) { /* A stalled activation expires; keep serving later requests. */ }
            catch (Exception)
            {
                if (cancellationToken.IsCancellationRequested) return;
                try { await Task.Delay(250, cancellationToken); }
                catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { return; }
            }
        }
    }

    private void OnExit(object sender, ExitEventArgs e)
    {
        _activationLifetime?.Cancel();
        _activationLifetime?.Dispose();
        if (_ownsInstance) _instance?.ReleaseMutex();
        _instance?.Dispose();
    }
}
