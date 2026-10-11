using Microsoft.Win32;
using Microsoft.Web.WebView2.Core;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Interop;
using Forms = System.Windows.Forms;

namespace FnCPN.Windows;

public partial class MainWindow : Window
{
    private const long MaximumStartupLogBytes = 1 << 20;
    private const int MaximumStartupLogMessageCharacters = 8192;
    private static readonly object StartupLogGate = new();
    private System.Drawing.Icon _trayIcon = LoadTrayIcon("offline");
    private string _trayIconVariant = "offline";
    private readonly Forms.NotifyIcon _tray = new() { Visible = false, Text = "FnCPN" };
    private Forms.ToolStripMenuItem? _trayPrimaryItem;
    private Forms.ToolStripMenuItem? _trayReloginItem;
    private Forms.ToolStripMenuItem? _trayRetryItem;
    private Forms.ToolStripMenuItem? _trayAutoStartItem;
    private readonly CancellationTokenSource _lifetime = new();
    private CancellationTokenSource? _loginLifetime;
    private long _authorizationGeneration;
    private Diagnostics? _diagnostics;
    private object? _operationError;
    private bool _allowExit;
    private bool _exitStarted;
    private bool _externalAuthorization;
    private bool _startInBackground;
    private bool _operationInFlight;
    private bool? _loginPanelRequested;
    private bool _privilegedUnavailablePrompted;
    private Window? _adminWindow;
    private Microsoft.Web.WebView2.Wpf.WebView2? _adminWebView;
    private bool _openingAdmin;
    private bool _adminNavigationHandlersAttached;
    private string? _adminProxyOrigin;
    private string? _authorizationRequestId;

    public MainWindow()
    {
        InitializeComponent();
        using (var appIcon = new System.Drawing.Icon(Path.Combine(AppContext.BaseDirectory, "FnCPN.ico"), 32, 32))
        using (var source = appIcon.ToBitmap())
        using (var canvas = new System.Drawing.Bitmap(32, 32))
        {
            using (var graphics = System.Drawing.Graphics.FromImage(canvas))
                graphics.DrawImage(source, new System.Drawing.Rectangle(0, 0, 32, 32), VisibleIconBounds(source), System.Drawing.GraphicsUnit.Pixel);
            var handle = canvas.GetHicon();
            try { Icon = Imaging.CreateBitmapSourceFromHIcon(handle, Int32Rect.Empty, System.Windows.Media.Imaging.BitmapSizeOptions.FromEmptyOptions()); Icon.Freeze(); }
            finally { DestroyIcon(handle); }
        }
        HeaderIcon.Source = LoadHeaderIcon();
        var informationalVersion = Assembly.GetEntryAssembly()?.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion;
        if (!string.IsNullOrWhiteSpace(informationalVersion))
        {
            var productVersion = informationalVersion.Split('+', 2)[0];
            if (!string.IsNullOrWhiteSpace(productVersion)) AppVersionText.Text = DetailsVersionText.Text = $"v{productVersion}";
        }
        _tray.Icon = _trayIcon;
        _tray.Visible = true;
        AppStrings.ApplyToTree(this);
        AutoStartBox.IsChecked = Registry.GetValue(@"HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run", "FnCPN", null) is string;
        BuildTrayMenu();
        _tray.DoubleClick += (_, _) => ShowWindow();
        SourceInitialized += (_, _) =>
        {
            var source = HwndSource.FromHwnd(new WindowInteropHelper(this).Handle);
            var taskbarCreated = RegisterWindowMessage("TaskbarCreated");
            source?.AddHook((IntPtr hwnd, int message, IntPtr wParam, IntPtr lParam, ref bool handled) =>
            {
                if (message == taskbarCreated)
                {
                    _tray.Visible = false;
                    _tray.Visible = true;
                }
                else if (message == 0x0011) // WM_QUERYENDSESSION
                {
                    handled = true;
                    return new IntPtr(1);
                }
                else if (message == 0x0016 && wParam != IntPtr.Zero) // WM_ENDSESSION
                {
                    handled = true;
                    _ = ExitApplicationAsync(stopDaemon: true);
                }
                return IntPtr.Zero;
            });
        };
        Loaded += async (_, _) =>
        {
            await EnsureDaemonAsync();
            await RefreshAsync();
            LoadingText.Visibility = Visibility.Collapsed;
            if (_diagnostics is null) LoginPanel.Visibility = Visibility.Visible;
            if (_startInBackground && !_externalAuthorization && !string.IsNullOrWhiteSpace(_diagnostics?.FnId)) Hide();
            else if (_startInBackground && string.IsNullOrWhiteSpace(_diagnostics?.FnId)) { ShowInTaskbar = true; ShowWindow(); }
            _ = PollAsync(_lifetime.Token);
        };
    }

    private static string TrayIconVariant(string state, bool needsLogin = false)
    {
        if (needsLogin) return "attention";
        return state switch
        {
            "LOCAL" or "DIRECT" or "RELAY" => "connected",
            "AUTHORIZING" or "PROBING" or "RECONNECTING" => "working",
            "ERROR" or "DAEMON_UNAVAILABLE" or "AUTH_REQUIRED" or "SERVER_UNAVAILABLE" => "attention",
            _ => "offline"
        };
    }

    private static string TrayState(string state, bool needsLogin, bool busy, bool? pendingAuthorizationBusy = null) =>
        pendingAuthorizationBusy.HasValue ? pendingAuthorizationBusy.Value ? "AUTHORIZING" : "AUTH_REQUIRED"
        : needsLogin ? "AUTH_REQUIRED" : busy ? "PROBING" : state;

    private static System.Windows.Media.Imaging.BitmapSource LoadHeaderIcon(string? baseDirectory = null)
    {
        using var appIcon = new System.Drawing.Icon(Path.Combine(baseDirectory ?? AppContext.BaseDirectory, "FnCPN.ico"), 256, 256);
        using var bitmap = appIcon.ToBitmap();
        var bounds = VisibleIconBounds(bitmap);
        var handle = bitmap.GetHicon();
        try
        {
            var source = Imaging.CreateBitmapSourceFromHIcon(handle, Int32Rect.Empty, System.Windows.Media.Imaging.BitmapSizeOptions.FromEmptyOptions());
            var cropped = new System.Windows.Media.Imaging.CroppedBitmap(source, new Int32Rect(bounds.X, bounds.Y, bounds.Width, bounds.Height));
            cropped.Freeze();
            return cropped;
        }
        finally { DestroyIcon(handle); }
    }

    private static System.Drawing.Rectangle VisibleIconBounds(System.Drawing.Bitmap bitmap)
    {
        var left = bitmap.Width; var top = bitmap.Height; var right = -1; var bottom = -1;
        for (var y = 0; y < bitmap.Height; y++)
        for (var x = 0; x < bitmap.Width; x++)
            if (bitmap.GetPixel(x, y).A > 16)
            { left = Math.Min(left, x); top = Math.Min(top, y); right = Math.Max(right, x); bottom = Math.Max(bottom, y); }
        return right < left ? new System.Drawing.Rectangle(0, 0, bitmap.Width, bitmap.Height)
            : System.Drawing.Rectangle.FromLTRB(left, top, right + 1, bottom + 1);
    }

    private static System.Drawing.Icon LoadTrayIcon(string variant, string? baseDirectory = null)
    {
        var color = variant switch
        {
            "connected" => System.Drawing.Color.FromArgb(34, 139, 88),
            "working" => System.Drawing.Color.FromArgb(40, 111, 210),
            "attention" => System.Drawing.Color.FromArgb(196, 72, 48),
            _ => System.Drawing.Color.FromArgb(112, 120, 130)
        };
        var imagePath = Path.Combine(baseDirectory ?? AppContext.BaseDirectory, $"Status-{variant}.png");
        using var source = new System.Drawing.Bitmap(imagePath);
        using var tinted = new System.Drawing.Bitmap(source.Width, source.Height, System.Drawing.Imaging.PixelFormat.Format32bppArgb);
        for (var y = 0; y < source.Height; y++)
        for (var x = 0; x < source.Width; x++)
        {
            var alpha = source.GetPixel(x, y).A;
            tinted.SetPixel(x, y, System.Drawing.Color.FromArgb(alpha, color));
        }

        using var canvas = new System.Drawing.Bitmap(32, 32, System.Drawing.Imaging.PixelFormat.Format32bppArgb);
        using (var graphics = System.Drawing.Graphics.FromImage(canvas))
        {
            graphics.Clear(System.Drawing.Color.Transparent);
            graphics.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.HighQuality;
            graphics.InterpolationMode = System.Drawing.Drawing2D.InterpolationMode.HighQualityBicubic;
            var bounds = VisibleIconBounds(source);
            var scale = Math.Min(32f / bounds.Width, 32f / bounds.Height);
            var width = bounds.Width * scale;
            var height = bounds.Height * scale;
            graphics.DrawImage(tinted, new System.Drawing.RectangleF((32f - width) / 2, (32f - height) / 2, width, height), bounds, System.Drawing.GraphicsUnit.Pixel);
        }

        var iconHandle = canvas.GetHicon();
        try
        {
            using var nativeIcon = System.Drawing.Icon.FromHandle(iconHandle);
            return (System.Drawing.Icon)nativeIcon.Clone();
        }
        finally { DestroyIcon(iconHandle); }
    }

    private void UpdateTrayIcon(string state, bool needsLogin = false)
    {
        var variant = TrayIconVariant(state, needsLogin);
        if (variant == _trayIconVariant) return;
        var replacement = LoadTrayIcon(variant);
        var previous = _trayIcon;
        _tray.Icon = replacement;
        _trayIcon = replacement;
        _trayIconVariant = variant;
        previous.Dispose();
    }

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyIcon(IntPtr icon);

    public void PrepareStartup(string[] args)
    {
        if (args.Contains("--background", StringComparer.OrdinalIgnoreCase)) _startInBackground = true;
        if (_startInBackground)
        {
            ShowInTaskbar = false;
            WindowState = WindowState.Minimized;
        }
    }

    public void HandleActivation(string[] args)
    {
        foreach (var argument in args)
        {
            if (!TryParseAuthorizationUri(argument, out var fnId, out var request)) continue;
            if (_authorizationRequestId == request && _loginLifetime is not null)
            {
                ShowWindow();
                ShowLogin();
                return;
            }
            var supersededRequest = ReplacedAuthorizationRequest(_authorizationRequestId, request);
            var replacingActiveLogin = _loginLifetime is not null && _authorizationRequestId != request;
            if (supersededRequest is not null || replacingActiveLogin)
            {
                _authorizationGeneration++;
                var previousLifetime = _loginLifetime;
                _loginLifetime = null;
                previousLifetime?.Cancel();
                LoginButton.IsEnabled = true;
                ReturnDetailsButton.IsEnabled = true;
                FnIdBox.IsEnabled = true;
                UsernameBox.IsEnabled = true;
                PasswordBox.IsEnabled = true;
                CancelLoginButton.Visibility = Visibility.Collapsed;
            }
            _externalAuthorization = request.Length > 0;
            _authorizationRequestId = request;
            CancelLoginButton.Visibility = _externalAuthorization ? Visibility.Visible : Visibility.Collapsed;
            if (supersededRequest is not null) _ = CancelAuthorizationAsync(supersededRequest);
            FnIdBox.Text = fnId;
            UsernameBox.Text = _diagnostics?.Username ?? "";
            PasswordBox.Clear();
            ShowWindow();
            ShowLogin();
            UpdateActions(_diagnostics);
            if (_diagnostics is null) UpdateTrayIcon(_externalAuthorization ? "AUTH_REQUIRED" : "UNCONFIGURED");
            break;
        }
        if (!_startInBackground && !args.Any(argument => Uri.TryCreate(argument, UriKind.Absolute, out var uri) && uri.Scheme == "fncpn" && uri.Host == "authorize")) ShowWindow();
    }

    public void ActivateFromExternalLaunch()
    {
        WriteStartupLog("A normal second-instance launch requested the existing window.");
        ShowWindow();
    }

    private async Task EnsureDaemonAsync()
    {
        WriteStartupLog("Checking client IPC.");
        try
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(1));
            _ = await IPC.CallAsync<JsonElement>("status", cancellationToken: timeout.Token);
            WriteStartupLog("Client IPC is available.");
            return;
        }
        catch (Exception ex) { WriteStartupLog($"Client IPC unavailable: {ex.GetType().Name}: {ex.Message}"); }

        // AppContext.BaseDirectory ends in a directory separator. Directory.GetParent()
        // treats that separator as a child segment on Windows and returns the UI
        // directory itself, which resolves fncpn.exe to the WPF executable.
        var installDirectory = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, ".."));
        var executable = Path.Combine(installDirectory, "fncpn.exe");
        WriteStartupLog($"Resolved client executable: {executable}; exists={File.Exists(executable)}.");
        if (!File.Exists(executable))
        {
            SetDaemonUnavailable(L("找不到 FnCPN Go 客户端组件。请修复安装。"));
            return;
        }
        try
        {
            var start = new ProcessStartInfo(executable)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                WorkingDirectory = installDirectory,
                WindowStyle = ProcessWindowStyle.Hidden,
                RedirectStandardOutput = true,
                RedirectStandardError = true
            };
            start.ArgumentList.Add("status");
            start.ArgumentList.Add("--json");
            using var bootstrap = Process.Start(start);
            if (bootstrap is null)
            {
                SetDaemonUnavailable(L("找不到 FnCPN Go 客户端组件。请修复安装。"));
                return;
            }
            WriteStartupLog($"Started client bootstrap PID {bootstrap.Id}.");
            var standardOutput = bootstrap.StandardOutput.ReadToEndAsync();
            var standardError = bootstrap.StandardError.ReadToEndAsync();
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
            timeout.CancelAfter(TimeSpan.FromSeconds(12));
            try { await bootstrap.WaitForExitAsync(timeout.Token); }
            catch (OperationCanceledException) when (!_lifetime.IsCancellationRequested)
            {
                try { bootstrap.Kill(); } catch (Exception) { }
                await bootstrap.WaitForExitAsync();
                WriteStartupLog("Client bootstrap timed out.");
                SetDaemonUnavailable(L("FnCPN 后台服务启动超时，请检查 Service 状态。"));
                return;
            }
            if (_lifetime.IsCancellationRequested) return;
            if (bootstrap.ExitCode != 0)
            {
                var details = await standardError;
                if (string.IsNullOrWhiteSpace(details)) details = await standardOutput;
                WriteStartupLog($"Client bootstrap exited {bootstrap.ExitCode}: {details.Trim()}");
                SetDaemonUnavailable(string.IsNullOrWhiteSpace(details)
                    ? L("FnCPN 后台服务未能启动，请检查 Service 状态。")
                    : details.Trim());
            }
            else WriteStartupLog("Client bootstrap completed successfully.");
        }
        catch (Exception ex)
        {
            WriteStartupLog($"Client bootstrap failed: {ex}");
            SetDaemonUnavailable(FormatException(ex));
        }
    }

    private static void WriteStartupLog(string message)
    {
        try
        {
            var directory = Path.Combine(GetLocalAppDataDirectory(), "FnCPN", "Logs");
            var path = Path.Combine(directory, "ui-startup.log");
            if (message.Length > MaximumStartupLogMessageCharacters)
                message = message[..MaximumStartupLogMessageCharacters];
            var entry = Encoding.UTF8.GetBytes($"{DateTimeOffset.Now:O} {message}{Environment.NewLine}");
            lock (StartupLogGate)
            {
                Directory.CreateDirectory(directory);
                if (File.Exists(path) && new FileInfo(path).Length + entry.Length > MaximumStartupLogBytes)
                    File.Move(path, path + ".1", overwrite: true);
                using var stream = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.Read);
                stream.Write(entry);
            }
        }
        catch (Exception) { }
    }

    private static string GetLocalAppDataDirectory()
    {
        var localAppData = Environment.GetEnvironmentVariable("LOCALAPPDATA");
        return string.IsNullOrWhiteSpace(localAppData)
            ? Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData)
            : localAppData;
    }

    private async Task PollAsync(CancellationToken cancellationToken)
    {
        ulong generation = 0;
        var retryDelay = TimeSpan.FromSeconds(1);
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                var watched = await IPC.CallAsync<StatusWatchResult>("watch-client-status", new { after = generation }, cancellationToken);
                generation = watched.Generation;
                var value = await IPC.CallAsync<Diagnostics>("diagnose", cancellationToken: cancellationToken);
                Dispatcher.Invoke(() => ApplyDiagnostics(value));
                retryDelay = TimeSpan.FromSeconds(1);
            }
            catch (OperationCanceledException) { return; }
            catch (Exception ex)
            {
                Dispatcher.Invoke(() => SetDaemonUnavailable(FormatException(ex)));
                try { await Task.Delay(retryDelay, cancellationToken); }
                catch (OperationCanceledException) { return; }
                await EnsureDaemonAsync();
                retryDelay = TimeSpan.FromSeconds(Math.Min(retryDelay.TotalSeconds * 2, 30));
                generation = 0;
            }
        }
    }

    private void SetDaemonUnavailable(string details)
    {
        if (_diagnostics is { FnId.Length: > 0 } current)
        {
            var failure = new RemoteError("UNAVAILABLE", L("客户端服务不可用"), details, "client.daemon");
            var status = current.Status with
            {
                State = "DAEMON_UNAVAILABLE",
                Path = null,
                Interface = null,
                LastHandshake = null,
                LastError = failure,
                LocalNetworkWaiting = false,
                LocalNetworkAccess = null
            };
            _diagnostics = current with
            {
                Status = status,
                PrivilegedAvailable = false,
                PrivilegedActive = false,
                PrivilegedDegraded = false,
                Errors = [failure]
            };
            StateText.Text = StateLabel(status.State);
            StatusError.Text = L("正在恢复客户端后台服务…") + "\n" + details;
            PathText.Text = PathLabel(null);
            HandshakeText.Text = "-";
            NetworkText.Text = (current.NetworkInterface ?? "-") +
                (current.Mtu > 0 ? $" · MTU {current.Mtu}" : "");
            PrivilegeText.Text = L("不可用");
            DiagnosticErrorsText.Text = DescribeFailure(failure);
        UpdateDiagnosticsJson();
            UpdateActions(_diagnostics);
            UpdateTrayIcon(status.State, needsLogin: true);
            _tray.Text = $"FnCPN · {L("后台服务不可用")}";
        }
        else LoginError.Text = details;
    }

    private async Task RefreshAsync()
    {
        try { ApplyDiagnostics(await IPC.CallAsync<Diagnostics>("diagnose", cancellationToken: _lifetime.Token)); }
        catch (Exception ex)
        {
            var message = FormatException(ex);
            if (_diagnostics is { FnId.Length: > 0 })
            {
                SetOperationError(ex);
                SetDaemonUnavailable(message);
            }
            else LoginError.Text = message;
        }
    }

    private void ApplyDiagnostics(Diagnostics value)
    {
        _diagnostics = value;
        if (_externalAuthorization)
            UsernameBox.Text = FillMissingAuthorizationUsername(UsernameBox.Text, value.Username);
        if (value.PrivilegedAvailable)
        {
            _privilegedUnavailablePrompted = false;
        }
        else if (ShouldNotifyServiceUnavailable(value.FnId, IsVisible, _privilegedUnavailablePrompted))
        {
            _privilegedUnavailablePrompted = true;
            _tray.ShowBalloonTip(
                5000,
                L("FnCPN 网络服务不可用"),
                L("请打开 FnCPN 查看诊断信息，或运行需要管理员权限的修复。"),
                Forms.ToolTipIcon.Warning);
        }
        var configured = !string.IsNullOrWhiteSpace(value.FnId);
        _loginPanelRequested ??= !configured;
        if (!configured) _loginPanelRequested = true;
        var showLoginPanel = ShouldShowLoginPanel(configured, _loginPanelRequested);
        var loginStatusMessage = ShouldShowCachedLoginStatus(configured, FnIdBox.Text, value.FnId)
            ? LoginStatusMessage(value.Status) : "";
        if (showLoginPanel && !string.IsNullOrEmpty(loginStatusMessage)) LoginError.Text = loginStatusMessage;
        if (ShouldClearOperationError(value.Status, showLoginPanel)) _operationError = null;
        if (!configured)
        {
            LoginPanel.Visibility = Visibility.Visible;
            DetailsPanel.Visibility = Visibility.Collapsed;
            return;
        }

        IdentityText.Text = value.FnId;
        var needsLogin = NeedsLogin(value.Status);
        StateText.Text = ConnectionStatusLabel(value.Status, needsLogin, _loginLifetime is not null);
        PathText.Text = PathLabel(value.Status.Path);
        AddressText.Text = value.ClientAddress ?? "-";
        NasText.Text = value.NasAddress ?? "-";
        HandshakeText.Text = value.HandshakeAge ?? "-";
        DirectText.Text = DirectLabel(value.Direct.Reason);
        ProbeDetailsText.Text = (value.Direct.CandidateCount > 0
            ? string.Format(L("IPv6 候选 {0}，已尝试 {1}"), value.Direct.CandidateCount, value.Direct.AttemptCount) : "") +
            (string.IsNullOrWhiteSpace(value.Direct.Endpoint) ? "" : $"\nUDP {value.Direct.Endpoint}");
        NetworkText.Text = (value.NetworkInterface ?? "-") +
            (value.Mtu > 0 ? $" · MTU {value.Mtu}" : "");
        var serviceLabel = !value.PrivilegedAvailable ? L("不可用") : value.PrivilegedDegraded ? L("降级") : value.PrivilegedActive ? L("活动") : L("就绪");
        PrivilegeText.Text = serviceLabel;
        foreach (var field in new[] { PathText, AddressText, NasText, HandshakeText, DirectText, NetworkText, PrivilegeText })
            field.ToolTip = field.Text;
        LanOverlapText.Text = value.LanOverlap ? L("本地路由冲突：仅保留 NAS 隧道路由") : "";
        var statusFailure = DescribeFailure(value.Status.LastError);
        StatusError.Text = value.Status.LocalNetworkWaiting
            ? L("正在等待局域网访问") + (string.IsNullOrWhiteSpace(value.Status.LocalNetworkAccess) ? "" : $" ({value.Status.LocalNetworkAccess})")
            : statusFailure;
        var diagnosticErrors = (value.Errors ?? [])
            .Select(DescribeFailure)
            .Append(ShouldShowDirectFailure(value.Status) ? DescribeFailure(value.Direct.LastError) : "")
            .Where(message => !string.IsNullOrWhiteSpace(message));
        DiagnosticErrorsText.Text = string.Join("\n", diagnosticErrors);
        var hasDiagnosticErrors = !string.IsNullOrWhiteSpace(DiagnosticErrorsText.Text);
        DiagnosticErrorsText.Foreground = hasDiagnosticErrors ? System.Windows.Media.Brushes.Firebrick : System.Windows.SystemColors.GrayTextBrush;
        if (!hasDiagnosticErrors && string.IsNullOrWhiteSpace(StatusError.Text) && !value.LanOverlap && string.IsNullOrWhiteSpace(ProbeDetailsText.Text)) DiagnosticErrorsText.Text = L("无异常");
        StatusError.Foreground = value.Status.LocalNetworkWaiting ? System.Windows.SystemColors.ControlTextBrush : System.Windows.Media.Brushes.Firebrick;
        AdminButton.Visibility = value.Administrator ? Visibility.Visible : Visibility.Collapsed;
        RepairServiceButton.Visibility = value.PrivilegedAvailable ? Visibility.Collapsed : Visibility.Visible;
        UpdateActions(value);
        _tray.Text = $"FnCPN · {PresentationStateLabel(value.Status.State, needsLogin)}";
        UpdateDiagnosticsJson();
        LoginPanel.Visibility = showLoginPanel ? Visibility.Visible : Visibility.Collapsed;
        DetailsPanel.Visibility = showLoginPanel ? Visibility.Collapsed : Visibility.Visible;
    }

    private async void OnLogin(object sender, RoutedEventArgs e)
    {
        if (_operationInFlight || _loginLifetime is not null) return;
        if (ShouldContinueRemoteFromLogin(_diagnostics?.Status))
        {
            await RunOperationAsync("continue-remote");
            return;
        }
        var fnId = NormalizeFnId(FnIdBox.Text);
        var username = UsernameBox.Text.Trim();
        var password = PasswordBox.Password;
        if (fnId is null)
        {
            LoginError.Text = L("FN Connect ID 格式不正确");
            FnIdBox.Focus();
            return;
        }
        if (string.IsNullOrWhiteSpace(username))
        {
            LoginError.Text = L("请输入 fnOS 用户名");
            UsernameBox.Focus();
            return;
        }
        if (string.IsNullOrEmpty(password))
        {
            LoginError.Text = L("请输入 fnOS 密码");
            PasswordBox.Focus();
            return;
        }
        FnIdBox.Text = fnId;

        LoginButton.IsEnabled = false;
        ReturnDetailsButton.IsEnabled = false;
        FnIdBox.IsEnabled = false;
        UsernameBox.IsEnabled = false;
        PasswordBox.IsEnabled = false;
        CancelLoginButton.Visibility = Visibility.Visible;
        LoginError.Text = L("正在登录并连接…");
        var generation = ++_authorizationGeneration;
        var loginLifetime = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
        _loginLifetime = loginLifetime;
        loginLifetime.CancelAfter(TimeSpan.FromMinutes(5));
        UpdateActions(_diagnostics);
        var token = loginLifetime.Token;
        var cliOwnsAuthorizationWait = !string.IsNullOrEmpty(_authorizationRequestId);
        string? requestId = _authorizationRequestId;
        _operationError = null;
        UpdateDiagnosticsJson();
        PasswordBox.Clear();
        try
        {
            if (string.IsNullOrEmpty(requestId))
            {
                var begun = await IPC.CallAsync<AuthorizationResult>("authorization-begin", new { fnId }, token);
                requestId = begun.RequestId;
                if (generation != _authorizationGeneration)
                {
                    await CancelAuthorizationAsync(requestId);
                    return;
                }
                _authorizationRequestId = requestId;
            }
            await IPC.CallAsync("authorize-native", new { requestId, fnId, username, password }, token);
            if (generation != _authorizationGeneration)
            {
                await CancelAuthorizationAsync(requestId);
                return;
            }
            // A CLI-launched transaction already has a CLI waiter. Authorization wait
            // consumes terminal state, so only the process that created a UI transaction
            // should consume its result here.
            if (!cliOwnsAuthorizationWait)
            {
                var result = await IPC.CallAsync<AuthorizationResult>("authorization-wait", new { requestId }, token);
                if (result.State != "succeeded") throw new AuthorizationFailureException(result.Error);
            }
            if (generation != _authorizationGeneration)
            {
                await CancelAuthorizationAsync(requestId);
                return;
            }
            _externalAuthorization = false;
            if (_authorizationRequestId == requestId) _authorizationRequestId = null;
            _loginPanelRequested = false;
            LoginError.Text = "";
            await RefreshAsync();
        }
        catch (OperationCanceledException)
        {
            if (generation == _authorizationGeneration)
            {
                LoginError.Text = L("登录已取消或超时。");
                _operationError = null;
                UpdateDiagnosticsJson();
            }
            await CancelAuthorizationAsync(requestId);
        }
        catch (Exception ex)
        {
            if (generation == _authorizationGeneration)
            {
                LoginError.Text = LoginFailureForException(ex);
                SetOperationError(ex);
            }
            await CancelAuthorizationAsync(requestId);
            if (generation == _authorizationGeneration && NeedsCertificateRepair(ex))
                await OfferCertificateRepairAsync(fnId, token);
        }
        finally
        {
            if (generation == _authorizationGeneration)
            {
                LoginButton.IsEnabled = true;
                ReturnDetailsButton.IsEnabled = true;
                FnIdBox.IsEnabled = true;
                UsernameBox.IsEnabled = true;
                PasswordBox.IsEnabled = true;
                CancelLoginButton.Visibility = Visibility.Collapsed;
            }
            if (ReferenceEquals(_loginLifetime, loginLifetime)) _loginLifetime = null;
            UpdateActions(_diagnostics);
            loginLifetime.Dispose();
        }
    }

    private async void OnCancelLogin(object sender, RoutedEventArgs e)
    {
        var requestId = _authorizationRequestId;
        _loginLifetime?.Cancel();
        await CancelAuthorizationAsync(requestId);
        CancelLoginButton.Visibility = Visibility.Collapsed;
        LoginError.Text = L("登录已取消。");
        UpdateActions(_diagnostics);
    }

    private async void OnReturnDetails(object sender, RoutedEventArgs e)
    {
        if (_loginLifetime is not null) return;
        await CancelAuthorizationAsync(_authorizationRequestId);
        CancelLoginButton.Visibility = Visibility.Collapsed;
        PasswordBox.Clear();
        UpdateActions(_diagnostics);
        ShowDetails();
    }

    private async Task CancelAuthorizationAsync(string? requestId)
    {
        if (string.IsNullOrEmpty(requestId)) return;
        if (_authorizationRequestId == requestId)
        {
            _authorizationRequestId = null;
            _externalAuthorization = false;
        }
        try
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(2));
            await IPC.CallAsync("authorization-cancel", new { requestId }, timeout.Token);
        }
        catch (Exception) { }
    }

    private static string? ReplacedAuthorizationRequest(string? currentRequestId, string incomingRequestId) =>
        !string.IsNullOrEmpty(currentRequestId) && currentRequestId != incomingRequestId ? currentRequestId : null;

    private async void OnPrimary(object sender, RoutedEventArgs e)
    {
        if (string.IsNullOrWhiteSpace(_diagnostics?.FnId))
        {
            ShowWindow();
            return;
        }
        if (_diagnostics.Status.LocalNetworkWaiting)
        {
            await RunOperationAsync("continue-remote");
            return;
        }
        if (NeedsLogin(_diagnostics.Status))
        {
            BeginRelogin();
            return;
        }
        var disconnect = _diagnostics?.Status.State is "LOCAL" or "DIRECT" or "RELAY" or "PROBING" or "RECONNECTING";
        await RunOperationAsync(disconnect ? "disconnect" : "connect");
    }

    private async void OnRetry(object sender, RoutedEventArgs e) => await RunOperationAsync("retry");

    private void OnRelogin(object sender, RoutedEventArgs e)
    {
        BeginRelogin();
    }

    private void BeginRelogin()
    {
        _operationError = null;
        UpdateDiagnosticsJson();
        FnIdBox.Text = _diagnostics?.FnId ?? "";
        UsernameBox.Text = _diagnostics?.Username ?? "";
        PasswordBox.Clear();
        LoginError.Text = LoginStatusMessage(_diagnostics?.Status);
        ShowLogin();
    }

    private void OnChangeServer(object sender, RoutedEventArgs e)
    {
        if (_operationInFlight || _loginLifetime is not null) return;
        var requestId = _authorizationRequestId;
        if (!string.IsNullOrEmpty(requestId)) _ = CancelAuthorizationAsync(requestId);
        _externalAuthorization = false;
        _authorizationRequestId = null;
        CancelLoginButton.Visibility = Visibility.Collapsed;
        _operationError = null;
        UpdateDiagnosticsJson();
        FnIdBox.Clear();
        UsernameBox.Clear();
        PasswordBox.Clear();
        LoginError.Text = "";
        UpdateActions(_diagnostics);
        ShowLogin();
    }

    private async Task RunOperationAsync(string method, object? parameters = null)
    {
        _operationInFlight = true;
        _operationError = null;
        UpdateDiagnosticsJson();
        UpdateActions(_diagnostics);
        try
        {
            _ = await IPC.CallAsync(method, parameters, _lifetime.Token);
            _operationError = null;
            await RefreshAsync();
        }
        catch (Exception ex)
        {
            StatusError.Text = FormatException(ex);
            SetOperationError(ex);
        }
        finally
        {
            _operationInFlight = false;
            UpdateActions(_diagnostics);
        }
    }

    private void OnOpenNas(object sender, RoutedEventArgs e)
    {
        if (_diagnostics?.NasAddress is not { Length: > 0 } address) return;
        try { OpenBrowser(new UriBuilder(Uri.UriSchemeHttp, address).Uri.AbsoluteUri); }
        catch (Exception ex) { ReportLocalActionFailure(ex); }
    }

    private async void OnRefresh(object sender, RoutedEventArgs e) => await RefreshAsync();

    private static string? NormalizeFnId(string input)
    {
        var value = input.Trim().ToLowerInvariant();
        var candidate = value;
        if (Uri.TryCreate(value, UriKind.Absolute, out var uri) && !string.IsNullOrWhiteSpace(uri.Host))
        {
            var host = uri.Host;
            if (host.Equals("fnos.net", StringComparison.OrdinalIgnoreCase))
            {
                var path = uri.GetComponents(UriComponents.Path, UriFormat.UriEscaped);
                var firstSegment = path.Split('/', StringSplitOptions.RemoveEmptyEntries).FirstOrDefault();
                candidate = firstSegment is null ? "" : Uri.UnescapeDataString(firstSegment);
            }
            else if (host.EndsWith(".fnos.net", StringComparison.OrdinalIgnoreCase))
            {
                candidate = host[..^".fnos.net".Length];
            }
        }
        else if (candidate.EndsWith(".fnos.net", StringComparison.Ordinal))
        {
            candidate = candidate[..^".fnos.net".Length];
        }

        if (candidate.Length is 0 or > 63 || candidate[0] == '-' || candidate[^1] == '-') return null;
        return candidate.All(character => character is >= 'a' and <= 'z' or >= '0' and <= '9' or '-')
            ? candidate
            : null;
    }

    private static bool TryParseAuthorizationUri(string value, out string fnId, out string requestId)
    {
        fnId = "";
        requestId = "";
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri) ||
            !uri.Scheme.Equals("fncpn", StringComparison.OrdinalIgnoreCase) ||
            !uri.Host.Equals("authorize", StringComparison.OrdinalIgnoreCase)) return false;

        string path;
        try { path = Uri.UnescapeDataString(uri.AbsolutePath.Trim('/')); }
        catch (UriFormatException) { return false; }
        var normalizedFnId = NormalizeFnId(path);
        var parsedRequestId = System.Web.HttpUtility.ParseQueryString(uri.Query).GetValues("request")?.FirstOrDefault();
        if (normalizedFnId is null || string.IsNullOrEmpty(parsedRequestId)) return false;

        fnId = normalizedFnId;
        requestId = parsedRequestId;
        return true;
    }

    private async void OnOpenAdmin(object sender, RoutedEventArgs e)
    {
        if (_adminWindow is not null) { _adminWindow.WindowState = WindowState.Normal; _adminWindow.Activate(); return; }
        if (_openingAdmin) return;
        _openingAdmin = true;
        try
        {
            var result = await IPC.CallAsync<JsonElement>("admin-proxy", cancellationToken: _lifetime.Token);
            if (!result.TryGetProperty("url", out var url) || url.GetString() is not { Length: > 0 } proxy || !IsValidAdminProxyUrl(proxy))
                throw new InvalidDataException(L("管理代理地址无效"));
            _adminProxyOrigin = new Uri(proxy, UriKind.Absolute).GetLeftPart(UriPartial.Authority);
            var view = new Microsoft.Web.WebView2.Wpf.WebView2();
            System.Windows.Automation.AutomationProperties.SetName(view, L("fnOS 管理后台"));
            var window = new Window { Title = L("FnCPN 管理后台"), Width = 1000, Height = 720,
                MinWidth = 640, MinHeight = 480, Icon = Icon, Content = view,
                WindowStartupLocation = WindowStartupLocation.CenterScreen };
            _adminWebView = view;
            _adminWindow = window;
            window.KeyDown += (_, args) => { if (args.Key == Key.Escape) window.Close(); };
            window.Closed += (_, _) =>
            {
                view.Dispose();
                _adminWebView = null;
                _adminWindow = null;
                _adminProxyOrigin = null;
                _adminNavigationHandlersAttached = false;
            };
            window.Show();
            if (view.CoreWebView2 is null)
            {
                var userDataFolder = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "FnCPN", "WebView2");
                Directory.CreateDirectory(userDataFolder);
                var environment = await CoreWebView2Environment.CreateAsync(userDataFolder: userDataFolder);
                await view.EnsureCoreWebView2Async(environment);
                if (!ReferenceEquals(_adminWindow, window)) return;
            }
            if (!_adminNavigationHandlersAttached)
            {
                view.CoreWebView2.NavigationStarting += OnAdminNavigationStarting;
                view.CoreWebView2.NewWindowRequested += OnAdminNewWindowRequested;
                _adminNavigationHandlersAttached = true;
            }
            view.CoreWebView2.Navigate(proxy);
        }
        catch (IPCException ex) when (FailureCode(ex.Details) == "AUTH_REQUIRED")
        {
            var answer = System.Windows.MessageBox.Show(
                L("请重新登录 FnCPN。当前隧道连接不受影响。"),
                L("管理后台登录已过期"),
                MessageBoxButton.YesNo,
                MessageBoxImage.Warning);
            if (answer == MessageBoxResult.Yes) OnRelogin(sender, e);
        }
        catch (Exception ex) { _adminWindow?.Close(); StatusError.Text = FormatException(ex); SetOperationError(ex); }
        finally { _openingAdmin = false; }
    }

    private async void OnRepairService(object sender, RoutedEventArgs e)
    {
        try
        {
            var serviceExists = await CheckServiceExistsAsync();
            var startResult = serviceExists ? await RunElevatedAsync("sc.exe", "start FnCPNClient") : 1060;
            if (startResult is 1060 or 2 or 193 or 1058)
            {
                var productCode = Registry.GetValue(@"HKEY_LOCAL_MACHINE\Software\FnCPN", "ProductCode", null) as string;
                if (!Guid.TryParse(productCode, out var parsedProductCode))
                    throw new InvalidOperationException(L("找不到 MSI 产品记录，请从原安装包运行修复。"));
                var repairResult = await RunElevatedAsync("msiexec.exe", $"/fa {parsedProductCode:B} /qb");
                if (repairResult == 1602) throw new OperationCanceledException(L("已取消 MSI 修复。"));
                if (repairResult != 0) throw new InvalidOperationException($"{L("FnCPN 安装修复失败")} (msiexec {repairResult})");
                // ServiceControl starts the LocalSystem service during MSI repair. Wait for
                // the daemon's authoritative diagnostic response instead of raising a second
                // UAC prompt based on a transient startup race.
                if (!await WaitForPrivilegedServiceAsync(TimeSpan.FromSeconds(15)))
                    throw new InvalidOperationException(L("安装修复已完成，但 FnCPN 网络服务未能就绪。请查看诊断信息或重新启动 Windows。"));
                await RefreshAsync();
                return;
            }
            if (startResult is not (0 or 1056)) throw new InvalidOperationException($"{L("启动 FnCPN 网络服务失败")} (sc.exe {startResult})");
            if (!await WaitForPrivilegedServiceAsync(TimeSpan.FromSeconds(10)))
                throw new InvalidOperationException(L("FnCPN 网络服务未能就绪。请查看诊断信息。"));
            await RefreshAsync();
        }
        catch (System.ComponentModel.Win32Exception ex) when (ex.NativeErrorCode == 1223)
        {
            StatusError.Text = L("已取消管理员授权。");
            _operationError = null;
            UpdateDiagnosticsJson();
        }
        catch (Exception ex) { StatusError.Text = FormatException(ex); SetOperationError(ex); }
    }

    private async Task<bool> CheckServiceExistsAsync()
    {
        using var process = Process.Start(new ProcessStartInfo("sc.exe", "query FnCPNClient")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            WindowStyle = ProcessWindowStyle.Hidden
        });
        if (process is null) throw new InvalidOperationException(L("无法启动 Windows Service 控制器"));
        await process.WaitForExitAsync(_lifetime.Token);
        if (process.ExitCode == 1060) return false;
        if (process.ExitCode != 0) throw new InvalidOperationException($"{L("无法查询 FnCPN 网络服务")} (sc.exe {process.ExitCode})");
        return true;
    }

    private async Task<bool> WaitForPrivilegedServiceAsync(TimeSpan timeout)
    {
        var deadline = DateTime.UtcNow + timeout;
        while (DateTime.UtcNow < deadline)
        {
            _lifetime.Token.ThrowIfCancellationRequested();
            try
            {
                var diagnostics = await IPC.CallAsync<Diagnostics>("diagnose", cancellationToken: _lifetime.Token);
                if (diagnostics.PrivilegedAvailable)
                {
                    ApplyDiagnostics(diagnostics);
                    return true;
                }
            }
            catch (OperationCanceledException) when (_lifetime.IsCancellationRequested)
            {
                throw;
            }
            catch
            {
                // The per-user daemon can be reconnecting while SCM starts the service.
            }
            await Task.Delay(250, _lifetime.Token);
        }
        return false;
    }

    private async Task<int> RunElevatedAsync(string fileName, string arguments)
    {
        using var process = Process.Start(new ProcessStartInfo(fileName, arguments)
        {
            UseShellExecute = true,
            Verb = "runas",
            WindowStyle = ProcessWindowStyle.Hidden
        });
        if (process is null) throw new InvalidOperationException(L("无法启动 Windows Service 控制器"));
        await process.WaitForExitAsync(_lifetime.Token);
        return process.ExitCode;
    }

    private void OnCopyDiagnostics(object sender, RoutedEventArgs e)
    {
        try
        {
            UpdateDiagnosticsJson();
            System.Windows.Clipboard.SetText(DiagnosticsText.Text);
        }
        catch (Exception ex) { ReportLocalActionFailure(ex); }
    }

    private void SetOperationError(Exception exception)
    {
        object fallback = new
        {
            code = "UNAVAILABLE",
            message = exception is AuthorizationFailureException ? L("授权没有完成") : FormatException(exception)
        };
        _operationError = fallback;
        if (exception is AuthorizationFailureException authorizationFailure)
        {
            _operationError = authorizationFailure.Failure ?? fallback;
        }
        else if (exception is IPCException ipcException)
        {
            try
            {
                _operationError = ipcException.Details.Deserialize<RemoteError>(new JsonSerializerOptions(JsonSerializerDefaults.Web)) ?? fallback;
            }
            catch (JsonException) { }
        }
        UpdateDiagnosticsJson();
    }

    private void UpdateDiagnosticsJson()
    {
        DiagnosticsText.Text = BuildDiagnosticsJson(_diagnostics, _operationError);
    }

    private static string BuildDiagnosticsJson(Diagnostics? diagnostics, object? operationError)
    {
        var options = new JsonSerializerOptions(JsonSerializerDefaults.Web) { WriteIndented = true };
        var payload = diagnostics is null
            ? new JsonObject()
            : JsonSerializer.SerializeToNode(diagnostics, options) as JsonObject ?? new JsonObject();
        if (operationError is not null) payload["operationError"] = JsonSerializer.SerializeToNode(operationError, options);
        return payload.ToJsonString(options);
    }
    private void OnOpenLog(object sender, RoutedEventArgs e)
    {
        try
        {
            var directory = Path.Combine(GetLocalAppDataDirectory(), "FnCPN", "Logs");
            Directory.CreateDirectory(directory);
            Process.Start(new ProcessStartInfo(directory) { UseShellExecute = true });
        }
        catch (Exception ex) { ReportLocalActionFailure(ex); }
    }

    private void ReportLocalActionFailure(Exception exception)
    {
        StatusError.Text = L("操作失败，请重试或查看诊断详情。");
        DiagnosticErrorsText.Text = FormatException(exception);
        SetOperationError(exception);
    }

    private void OnAutoStartChanged(object sender, RoutedEventArgs e)
    {
        if (!IsLoaded) return;
        try
        {
            using var run = Registry.CurrentUser.CreateSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run");
            if (AutoStartBox.IsChecked == true)
                run.SetValue("FnCPN", $"\"{Environment.ProcessPath}\" --background");
            else run.DeleteValue("FnCPN", false);
        }
        catch (Exception ex)
        {
            StatusError.Text = FormatException(ex);
            AutoStartBox.IsChecked = Registry.GetValue(@"HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run", "FnCPN", null) is string;
        }
        if (_trayAutoStartItem is not null) _trayAutoStartItem.Checked = AutoStartBox.IsChecked == true;
    }

    private void BuildTrayMenu()
    {
        var menu = new Forms.ContextMenuStrip();
        menu.Items.Add(L("打开 FnCPN"), null, (_, _) => ShowWindow());
        _trayPrimaryItem = new Forms.ToolStripMenuItem(L("连接"), null, async (_, _) => await OnPrimaryFromTrayAsync());
        _trayReloginItem = new Forms.ToolStripMenuItem(L("重新登录"), null, (_, _) => { ShowWindow(); BeginRelogin(); });
        _trayRetryItem = new Forms.ToolStripMenuItem(L("重试连接"), null, async (_, _) => await RunOperationAsync("retry"));
        _trayAutoStartItem = new Forms.ToolStripMenuItem(L("登录 Windows 后启动 FnCPN"))
        {
            CheckOnClick = true,
            Checked = AutoStartBox.IsChecked == true
        };
        _trayAutoStartItem.CheckedChanged += (_, _) => AutoStartBox.IsChecked = _trayAutoStartItem.Checked;
        menu.Items.Add(_trayPrimaryItem);
        menu.Items.Add(_trayReloginItem);
        menu.Items.Add(_trayRetryItem);
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(_trayAutoStartItem);
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(L("退出"), null, (_, _) => ExitApplication());
        _tray.ContextMenuStrip = menu;
    }

    private async Task OnPrimaryFromTrayAsync()
    {
        if (string.IsNullOrWhiteSpace(_diagnostics?.FnId))
        {
            ShowWindow();
            return;
        }
        if (_diagnostics.Status.LocalNetworkWaiting)
        {
            await RunOperationAsync("continue-remote");
            return;
        }
        if (NeedsLogin(_diagnostics.Status))
        {
            ShowWindow();
            BeginRelogin();
            return;
        }
        var disconnect = _diagnostics?.Status.State is "LOCAL" or "DIRECT" or "RELAY" or "PROBING" or "RECONNECTING";
        await RunOperationAsync(disconnect ? "disconnect" : "connect");
    }

    private void UpdateActions(Diagnostics? value)
    {
        if (value is null) return;
        var status = value.Status;
        var needsLogin = NeedsLogin(status);
        var waitingForLocalNetwork = ShouldContinueRemoteFromLogin(status);
        var busy = _operationInFlight || _loginLifetime is not null || status.State is "AUTHORIZING" or "PROBING" or "RECONNECTING";
        LoginButton.Content = LoginPrimaryLabel(status);
        LoginButton.IsEnabled = !busy || (waitingForLocalNetwork && !_operationInFlight && _loginLifetime is null);
        var trayState = TrayState(
            status.State,
            needsLogin,
            busy,
            _externalAuthorization && !string.IsNullOrEmpty(_authorizationRequestId) ? _loginLifetime is not null : null);
        UpdateTrayIcon(trayState);
        var connected = status.State is "LOCAL" or "DIRECT" or "RELAY";
        var primaryLabel = waitingForLocalNetwork
            ? L("继续远程连接")
            : needsLogin ? L("重新登录") : connected ? L("断开") : L("连接");

        PrimaryButton.Content = primaryLabel;
        PrimaryButton.IsEnabled = !busy || waitingForLocalNetwork;
        RetryButton.Visibility = needsLogin ? Visibility.Collapsed : Visibility.Visible;
        RetryButton.IsEnabled = !busy && !waitingForLocalNetwork;
        ReloginButton.Visibility = needsLogin ? Visibility.Collapsed : Visibility.Visible;
        ReloginButton.IsEnabled = !busy;
        ChangeServerButton.IsEnabled = !busy;

        if (_trayPrimaryItem is not null)
        {
            _trayPrimaryItem.Text = primaryLabel;
            _trayPrimaryItem.Enabled = !busy || waitingForLocalNetwork;
        }
        if (_trayReloginItem is not null)
        {
            _trayReloginItem.Visible = !needsLogin && !string.IsNullOrWhiteSpace(value.FnId);
            _trayReloginItem.Enabled = !busy;
        }
        if (_trayRetryItem is not null)
        {
            _trayRetryItem.Visible = !needsLogin && !string.IsNullOrWhiteSpace(value.FnId);
            _trayRetryItem.Enabled = !busy && !waitingForLocalNetwork;
        }
        if (_trayAutoStartItem is not null) _trayAutoStartItem.Checked = AutoStartBox.IsChecked == true;
    }

    private static bool NeedsLogin(ClientStatus status)
    {
        var failure = status.LastError;
        if (status.State == "AUTH_REQUIRED" || failure?.Code is "AUTH_REQUIRED" or "DEVICE_REVOKED") return true;
        if (failure?.Code != "PERMISSION_DENIED") return false;
        return failure.Operation?.StartsWith("credentials.", StringComparison.Ordinal) != true &&
            failure.Operation?.StartsWith("privileged.", StringComparison.Ordinal) != true &&
            (failure.HttpStatus == 403 || failure.Operation?.StartsWith("authorization.", StringComparison.Ordinal) == true);
    }

    private static bool ShouldContinueRemoteFromLogin(ClientStatus? status) => status?.LocalNetworkWaiting == true;

    private static bool ShouldNotifyServiceUnavailable(string? fnId, bool windowVisible, bool alreadyPrompted) =>
        !string.IsNullOrWhiteSpace(fnId) && !windowVisible && !alreadyPrompted;

    private static bool ShouldShowLoginPanel(bool configured, bool? requested) => !configured || requested == true;

    private static bool ShouldClearOperationError(ClientStatus status, bool showingLoginPanel) =>
        !showingLoginPanel && status.LastError is null && status.State is ("LOCAL" or "DIRECT" or "RELAY");

    private static bool ShouldShowDirectFailure(ClientStatus status) =>
        status.LocalNetworkWaiting || status.LastError is not null || status.State is not ("LOCAL" or "DIRECT" or "RELAY");

    private static string FillMissingAuthorizationUsername(string currentUsername, string? savedUsername) =>
        string.IsNullOrWhiteSpace(currentUsername) ? savedUsername ?? "" : currentUsername;

    private static string LoginStatusMessage(ClientStatus? status) => status is null
        ? ""
        : status.LocalNetworkWaiting
            ? L("正在等待局域网访问权限")
            : LoginFailureMessage(status.LastError);

    private static bool ShouldShowCachedLoginStatus(bool configured, string? enteredFnId, string? savedFnId) =>
        configured && string.Equals(NormalizeFnId(enteredFnId ?? ""), savedFnId, StringComparison.Ordinal);

    private static string LoginFailureMessage(RemoteError? failure)
    {
        if (failure is null) return "";
        return failure.Code switch
        {
            "AUTH_REQUIRED" => failure.Operation?.Contains("native_session.authenticate", StringComparison.Ordinal) == true
                ? L("用户名或密码无效，请重试") : L("登录已失效，请重新输入密码"),
            "PERMISSION_DENIED" => L("该账号权限不足，无法完成授权"),
            "UNAVAILABLE" or "TIMEOUT" => L("暂时无法连接 fnOS，请检查网络后重试"),
            "FAILED_PRECONDITION" when failure.RemoteCode == "TWO_FACTOR_REQUIRED" => L("该账号需要两步验证，当前版本暂不支持"),
            "FAILED_PRECONDITION" when failure.RemoteCode == "ISRG_ROOT_X1_MISSING" => L("系统缺少 ISRG Root X1 根证书，无法验证 fnOS 的 HTTPS 连接。"),
            "FAILED_PRECONDITION" => DescribeFailure(failure),
            _ => DescribeFailure(failure)
        };
    }

    private static bool NeedsCertificateRepair(Exception exception)
    {
        var failure = exception is AuthorizationFailureException authorizationFailure ? authorizationFailure.Failure
            : exception is IPCException ipcFailure ? ReadRemoteError(ipcFailure.Details) : null;
        return failure is { Code: "FAILED_PRECONDITION", RemoteCode: "ISRG_ROOT_X1_MISSING", Operation: "native_session.connect" };
    }

    private async Task OfferCertificateRepairAsync(string fnId, CancellationToken token)
    {
        var answer = System.Windows.MessageBox.Show(this,
            L("当前系统缺少 Let’s Encrypt 的 ISRG Root X1 根证书，无法验证 fnOS 的 HTTPS 连接。是否安装官方根证书到当前用户的证书库？这会影响当前用户下其他应用的证书验证。"),
            L("安装官方根证书？"), MessageBoxButton.YesNo, MessageBoxImage.Question, MessageBoxResult.No);
        if (answer != MessageBoxResult.Yes || token.IsCancellationRequested) return;
        try
        {
            LoginError.Text = L("正在安装并验证根证书…");
            await IPC.CallAsync("install-isrg-root-x1", new { fnId, confirmed = true }, token);
            _operationError = null;
            UpdateDiagnosticsJson();
            LoginError.Text = L("根证书已安装并验证，请重新输入密码并登录。");
        }
        catch (Exception error)
        {
            LoginError.Text = L("根证书安装或验证失败。") + " " + FormatException(error);
            SetOperationError(error);
        }
    }

    private static string LoginFailureForException(Exception exception)
    {
        if (exception is AuthorizationFailureException authorizationFailure)
        {
            return authorizationFailure.Failure is { } failure
                ? LoginFailureMessage(failure)
                : L("授权没有完成");
        }
        var remoteFailure = exception is IPCException ipcException ? ReadRemoteError(ipcException.Details) : null;
        var message = LoginFailureMessage(remoteFailure);
        return string.IsNullOrEmpty(message) ? FormatException(exception) : message;
    }

    private static RemoteError? ReadRemoteError(JsonElement details)
    {
        try { return details.Deserialize<RemoteError>(new JsonSerializerOptions(JsonSerializerDefaults.Web)); }
        catch (JsonException) { return null; }
    }

    private static string LoginPrimaryLabel(ClientStatus? status) =>
        L(ShouldContinueRemoteFromLogin(status) ? "继续远程连接" : "登录并连接");

    private void OnClosing(object? sender, System.ComponentModel.CancelEventArgs e)
    {
        if (_allowExit) return;
        e.Cancel = true;
        Hide();
    }

    private void ShowWindow()
    {
        Show();
        WindowState = WindowState.Normal;
        Activate();
    }

    private void ShowLogin()
    {
        _adminWindow?.Close();
        _loginPanelRequested = true;
        DetailsPanel.Visibility = Visibility.Collapsed;
        LoginPanel.Visibility = Visibility.Visible;
        ReturnDetailsButton.Visibility = string.IsNullOrWhiteSpace(_diagnostics?.FnId) ? Visibility.Collapsed : Visibility.Visible;
        Show();
        FnIdBox.Focus();
    }

    private void ShowDetails()
    {
        _loginPanelRequested = false;
        LoginPanel.Visibility = Visibility.Collapsed;
        DetailsPanel.Visibility = Visibility.Visible;
    }

    private async void ExitApplication()
    {
        await ExitApplicationAsync(stopDaemon: true);
    }

    private void OnExit(object sender, RoutedEventArgs e) => ExitApplication();

    private async Task ExitApplicationAsync(bool stopDaemon)
    {
        if (_exitStarted) return;
        _exitStarted = true;
        _adminWindow?.Close();
        try
        {
            if (stopDaemon)
            {
                using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(3));
                _ = await IPC.CallAsync("shutdown-client-daemon", cancellationToken: timeout.Token);
            }
        }
        catch (Exception) { }
        _allowExit = true;
        _lifetime.Cancel();
        _tray.Visible = false;
        _tray.Dispose();
        _trayIcon.Dispose();
        System.Windows.Application.Current.Shutdown();
    }

    private static void OpenBrowser(string target) => Process.Start(new ProcessStartInfo(target) { UseShellExecute = true });

    [System.Runtime.InteropServices.DllImport("user32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
    private static extern uint RegisterWindowMessage(string message);

    private static string L(string value) => AppStrings.Get(value);

    private sealed class AuthorizationFailureException(RemoteError? failure) : Exception
    {
        public RemoteError? Failure { get; } = failure;
    }

    private static bool IsValidAdminProxyUrl(string value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri) ||
            !string.Equals(uri.Scheme, Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) ||
            !IPAddress.TryParse(uri.Host, out var address) || !IPAddress.IsLoopback(address) ||
            uri.IsDefaultPort || uri.UserInfo.Length != 0 || uri.AbsolutePath != "/app/fncpn" ||
            uri.Fragment.Length != 0)
        {
            return false;
        }

        const string tokenPrefix = "?fncpn_proxy=";
        var query = uri.Query;
        if (!query.StartsWith(tokenPrefix, StringComparison.Ordinal) || query.Length != tokenPrefix.Length + 64)
            return false;
        foreach (var character in query.AsSpan(tokenPrefix.Length))
        {
            if (!Uri.IsHexDigit(character)) return false;
        }
        return true;
    }

    private static bool IsAllowedAdminNavigation(string value, string? expectedOrigin)
    {
        if (string.IsNullOrWhiteSpace(expectedOrigin) ||
            !Uri.TryCreate(expectedOrigin, UriKind.Absolute, out var expected) ||
            !Uri.TryCreate(value, UriKind.Absolute, out var target) ||
            !string.Equals(expected.Scheme, Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) ||
            !string.Equals(target.Scheme, expected.Scheme, StringComparison.OrdinalIgnoreCase) ||
            !IPAddress.TryParse(expected.Host, out var expectedAddress) || !IPAddress.IsLoopback(expectedAddress) ||
            !IPAddress.TryParse(target.Host, out var targetAddress) || !IPAddress.IsLoopback(targetAddress) ||
            !expectedAddress.Equals(targetAddress) || expected.Port != target.Port ||
            target.UserInfo.Length != 0 || target.IsDefaultPort)
        {
            return false;
        }

        return target.AbsolutePath.Equals("/app/fncpn", StringComparison.Ordinal) ||
            target.AbsolutePath.StartsWith("/app/fncpn/", StringComparison.Ordinal);
    }

    private void OnAdminNavigationStarting(object? sender, CoreWebView2NavigationStartingEventArgs e)
    {
        if (IsAllowedAdminNavigation(e.Uri, _adminProxyOrigin)) return;
        e.Cancel = true;
        if (e.IsUserInitiated) OpenExternalAdminLink(e.Uri);
    }

    private void OnAdminNewWindowRequested(object? sender, CoreWebView2NewWindowRequestedEventArgs e)
    {
        e.Handled = true;
        if (IsAllowedAdminNavigation(e.Uri, _adminProxyOrigin))
        {
            _adminWebView?.CoreWebView2?.Navigate(e.Uri);
        }
        else if (e.IsUserInitiated)
        {
            OpenExternalAdminLink(e.Uri);
        }
    }

    private void OpenExternalAdminLink(string value)
    {
        var sanitized = SanitizeExternalAdminLink(value);
        if (sanitized is null) return;
        try { OpenBrowser(sanitized); }
        catch (Exception ex)
        {
            StatusError.Text = FormatException(ex);
            SetOperationError(ex);
        }
    }

    private static string? SanitizeExternalAdminLink(string value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var target) ||
            (!target.Scheme.Equals(Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) &&
             !target.Scheme.Equals(Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase)) || target.UserInfo.Length != 0)
        {
            return null;
        }

        var query = System.Web.HttpUtility.ParseQueryString(target.Query);
        query.Remove("fncpn_proxy");
        return new UriBuilder(target) { Query = query.ToString() ?? "" }.Uri.AbsoluteUri;
    }

    private static string FormatException(Exception exception) => exception switch
    {
        IPCException ipcException => DescribeFailure(ipcException.Details),
        TimeoutException => L("操作超时，请重试。"),
        _ => L(exception.Message)
    };

    private static string DescribeFailure(JsonElement details)
    {
        if (details.ValueKind != JsonValueKind.Object) return "";
        try { return DescribeFailure(details.Deserialize<RemoteError>(new JsonSerializerOptions(JsonSerializerDefaults.Web))); }
        catch (JsonException) { return L("操作失败，请重试或查看诊断详情。"); }
    }

    private static string? FailureCode(JsonElement details) =>
        details.ValueKind == JsonValueKind.Object && details.TryGetProperty("code", out var code) && code.ValueKind == JsonValueKind.String
            ? code.GetString()
            : null;

    private static string DescribeFailure(RemoteError? failure)
    {
        if (failure is null) return "";
        var summary = failure.Code switch
        {
            "INVALID_ARGUMENT" => L("请求参数无效，请检查输入。"),
            "AUTH_REQUIRED" => failure.Operation?.Contains("native_session.authenticate", StringComparison.Ordinal) == true
                ? L("用户名或密码无效，请重试。") : L("登录已失效，请重新登录。"),
            "PERMISSION_DENIED" => L("权限不足，无法完成操作。"),
            "NOT_FOUND" => L("目标不存在，请刷新后重试。"),
            "ALREADY_EXISTS" or "CONFLICT" => L("连接配置冲突，请刷新后重试。"),
            "FAILED_PRECONDITION" => failure.RemoteCode == "TWO_FACTOR_REQUIRED"
                ? L("该账号需要两步验证，当前版本暂不支持。") : L("连接配置前置条件未满足。"),
            "DEVICE_REVOKED" => L("设备授权已撤销，请重新登录。"),
            "UNAVAILABLE" => L("服务暂不可用，请稍后重试。"),
            "SERVER_UNAVAILABLE" => L("未找到 FnCPN 服务，请在 fnOS 安装或启动 FnCPN 后重新连接。"),
            "TIMEOUT" => L("操作超时，请重试。"),
            "CANCELED" => L("操作已取消。"),
            "DISCOVERY_FAILED" => L("网络地址发现失败，请检查网络。"),
            "PROTOCOL_ERROR" => L("服务响应格式无效，请重试。"),
            "RESOURCE_EXHAUSTED" => L("资源不足，请稍后重试。"),
            "VERSION_INCOMPATIBLE" => failure.UpgradeTarget == "client"
                ? L("客户端版本不兼容，请升级客户端。") : L("服务端版本不兼容，请升级服务端。"),
            _ => L("操作失败，请重试或查看诊断详情。")
        };
        var details = new List<string> { failure.Code };
        if (!string.IsNullOrWhiteSpace(failure.Operation)) details.Add($"[{failure.Operation}]");
        if (failure.HttpStatus is int httpStatus) details.Add($"HTTP {httpStatus}");
        if (!string.IsNullOrWhiteSpace(failure.RemoteCode)) details.Add($"remote {failure.RemoteCode}");
        if (!string.IsNullOrWhiteSpace(failure.Detail)) details.Add(failure.Detail);
        if (!string.IsNullOrWhiteSpace(failure.RequestId)) details.Add($"request {failure.RequestId}");
        if (failure.Code == "VERSION_INCOMPATIBLE") details.Add($"client {failure.ClientVersion ?? "?"}, server {failure.ServerVersion ?? "?"}");
        return summary + "\n" + L("详细信息") + ": " + string.Join(": ", details);
    }

    private static string DirectLabel(string? reason) => reason switch
    {
        "connected" => L("直接连接已建立"),
        "local_network" => L("同一局域网，无需隧道"),
        "no_local_ipv6" => L("本机网络不支持 IPv6"),
        "no_server_ipv6" => L("NAS 未提供可用的公网 IPv6"),
        "server_disabled" => L("NAS 已禁用公网 IPv6 访问"),
        "handshake_failed" => L("UDP 握手失败，已回退中继"),
        "discovery_failed" => L("地址发现失败，已使用中继"),
        "cooldown" => L("等待下一次直连检测"),
        "probing" or "checking" => L("正在检测"),
        _ => L("尚未检测")
    };

    private static string LocalAccessLabel(string state) => state switch
    {
        "checking" => L("检测中"),
        "waiting" => L("等待授权"),
        "allowed" => L("已允许"),
        "unavailable" => L("不可用"),
        _ => state
    };

    private static string StateLabel(string? state) => state switch
    {
        "LOCAL" => L("已在同一局域网"), "DIRECT" => L("IPv6 直连"), "RELAY" => L("FN Connect 中继"),
        "AUTHORIZING" => L("正在登录"), "PROBING" => L("正在检测网络"), "RECONNECTING" => L("正在重新连接"),
        "UNCONFIGURED" => L("尚未连接"), "PAUSED" => L("已断开"), "AUTH_REQUIRED" => L("需要重新登录"),
        "DAEMON_UNAVAILABLE" => L("客户端服务不可用"),
        "SERVER_UNAVAILABLE" => L("未找到 FnCPN 服务"), "ERROR" => L("连接失败"), _ => state ?? L("状态未知")
    };

    private static string PresentationStateLabel(string? state, bool needsLogin) =>
        StateLabel(needsLogin ? "AUTH_REQUIRED" : state);

    private static string ConnectionStatusLabel(ClientStatus status, bool needsLogin, bool loginInFlight) =>
        status.LocalNetworkWaiting ? L("正在等待局域网访问权限")
        : loginInFlight ? L("正在登录")
        : PresentationStateLabel(status.State, needsLogin);

    private static string PathLabel(string? path) => path switch
    {
        "local" => L("局域网直达"), "direct" => L("IPv6 直连"), "relay" => L("WSS 中继"), _ => path ?? L("未连接")
    };
}
