#define FNCPN_NATIVE_TEST
#include "main.cpp"
#include <iostream>
#include <stdexcept>

LRESULT TestSendMessage(HWND window,UINT message,WPARAM parameter,LPARAM extra) {
    if(parameter) switch(message) {
    case kStatusReady: parameter=RegisterPayload(message,reinterpret_cast<RefreshResult*>(parameter)); break;
    case kLoginResult: parameter=RegisterPayload(message,reinterpret_cast<LoginResult*>(parameter)); break;
    case kAccessProbeReady: parameter=RegisterPayload(message,reinterpret_cast<AccessProbeResult*>(parameter)); break;
    case kAuthorizationStarted: case kOperationReady: parameter=RegisterPayload(message,reinterpret_cast<std::wstring*>(parameter)); break;
    case kAdminReady: parameter=RegisterPayload(message,reinterpret_cast<std::pair<std::wstring,std::wstring>*>(parameter)); break;
    case kActivationReceived: parameter=RegisterPayload(message,reinterpret_cast<std::vector<std::wstring>*>(parameter)); break;
    }
    return SendMessageW(window,message,parameter,extra);
}
#define SendMessageW(...) TestSendMessage(__VA_ARGS__)

void Check(bool condition, const char* label) {
    if (!condition) throw std::runtime_error(label);
}
struct LifetimeProbe { int* destroyed; ~LifetimeProbe(){++*destroyed;} };

void CheckPipeRoundTrip() {
    DWORD session = 0; ProcessIdToSessionId(GetCurrentProcessId(), &session);
    const auto name = L"\\\\.\\pipe\\FnCPN.Client.v1." + std::to_wstring(session) + L".native-test." + std::to_wstring(GetCurrentProcessId());
    HANDLE pipe = CreateNamedPipeW(name.c_str(), PIPE_ACCESS_DUPLEX | FILE_FLAG_FIRST_PIPE_INSTANCE,
        PIPE_TYPE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 5000, nullptr);
    Check(pipe != INVALID_HANDLE_VALUE, "isolated pipe creation");
    std::thread server([pipe] {
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        if (ConnectNamedPipe(pipe, nullptr) || GetLastError() == ERROR_PIPE_CONNECTED) {
            std::array<uint8_t, 4> header{};
            if (TransferPipeBytes(pipe, header.data(), 4, false)) {
                std::vector<uint8_t> bytes(ReadU32(header));
                if (TransferPipeBytes(pipe, bytes.data(), static_cast<DWORD>(bytes.size()), false)) {
                    auto request = JsonObject::Parse(winrt::to_hstring(std::string(bytes.begin(), bytes.end())));
                    JsonObject reply;
                    reply.Insert(L"id", request.GetNamedValue(L"id"));
                    reply.Insert(L"version", JsonValue::CreateNumberValue(kIpcVersion));
                    reply.Insert(L"productVersion", JsonValue::CreateStringValue(FNCPN_PRODUCT_VERSION));
                    reply.Insert(L"ok", JsonValue::CreateBooleanValue(true));
                    auto result = JsonObject::Parse(L"{\"state\":\"PAUSED\",\"futureField\":true}");
                    reply.Insert(L"result", result);
                    const auto text = winrt::to_string(reply.Stringify());
                    WriteU32(header, static_cast<uint32_t>(text.size()));
                    // Deliberately fragment both the header and JSON frame.
                    for (auto byte : header) TransferPipeBytes(pipe, &byte, 1, true);
                    for (auto ch : text) TransferPipeBytes(pipe, &ch, 1, true);
                    FlushFileBuffers(pipe);
                }
            }
        }
        DisconnectNamedPipe(pipe); CloseHandle(pipe); winrt::uninit_apartment();
    });
    try {
        auto reply = CallIpc(L"status");
        Check(JsonString(reply.GetNamedObject(L"result"), L"state") == L"PAUSED", "fragmented IPC round trip");
    } catch (...) { server.join(); throw; }
    server.join();
}

int main(int argc, char** argv) {
    winrt::init_apartment(winrt::apartment_type::single_threaded);
    try {
        Check(NormalizeFnId(L"  Example.FNOS.NET ") == L"example", "FN ID normalization");
        for (const auto* value : {L"", L"-host", L"host-", L"a/b", L"a b", L"a.example"})
            Check(NormalizeFnId(value).empty(), "invalid FN ID");
        std::wstring fnId, request;
        Check(ParseAuthorizationUri(L"FNCPN://AUTHORIZE/demo?request=abc-123", fnId, request) && fnId == L"demo" && request == L"abc-123", "CLI activation");
        for (const auto* value : {L"fncpn://authorize/demo", L"fncpn://authorize/demo?notrequest=abc", L"fncpn://authorize/demo?request=a&request=b", L"fncpn://authorize/demo?request=a#fragment", L"https://authorize/demo?request=a"})
            Check(!ParseAuthorizationUri(value, fnId, request), "invalid activation");
        g_adminUrl = L"http://127.0.0.1:12345/app/fncpn?fncpn_proxy=" + std::wstring(64, L'a');
        Check(IsValidProxyUrl(g_adminUrl), "proxy bootstrap");
        Check(IsAllowedAdminUrl(g_adminUrl), "bootstrap query stays inside WebView");
        Check(SanitizeExternalLink(L"http://127.0.0.1:12345/app/fncpn").empty(), "local management URL never opens externally");
        Check(!IsValidProxyUrl(g_adminUrl + L"&extra=1"), "ambiguous bootstrap query");
        Check(!IsValidProxyUrl(g_adminUrl + L"#fragment"), "bootstrap fragment");
        Check(IsAllowedAdminUrl(L"http://127.0.0.1:12345/app/fncpn/settings?tab=1"), "cookie navigation without bearer");
        for (const auto* value : {L"http://127.0.0.1:12346/app/fncpn", L"http://example.com:12345/app/fncpn", L"http://127.0.0.1:12345/app/fncpn-other", L"http://user@127.0.0.1:12345/app/fncpn", L"file:///app/fncpn"})
            Check(!IsAllowedAdminUrl(value), "navigation boundary");

        Check(SanitizeExternalLink(L"https://example.com/help?topic=network&FNCPN_PROXY=test&fncpn%5fproxy=test#section") == L"https://example.com/help?topic=network#section", "external link token redaction");
        Check(SanitizeExternalLink(L"file:///test").empty(), "external scheme rejection");
        JsonObject envelope;
        envelope.Insert(L"version", JsonValue::CreateNumberValue(kIpcVersion));
        envelope.Insert(L"productVersion", JsonValue::CreateStringValue(FNCPN_PRODUCT_VERSION));
        envelope.Insert(L"id", JsonValue::CreateStringValue(L"request"));
        envelope.Insert(L"ok", JsonValue::CreateBooleanValue(true));
        envelope.Insert(L"result", JsonObject{});
        envelope.Insert(L"futureField", JsonValue::CreateBooleanValue(true));
        ValidateEnvelope(envelope, L"request");
        for (const auto* field : {L"version", L"productVersion", L"id", L"ok", L"result"}) {
            auto invalid = JsonObject::Parse(envelope.Stringify()); invalid.Remove(field);
            bool rejected = false;
            try { ValidateEnvelope(invalid, L"request"); } catch (...) { rejected = true; }
            Check(rejected, "missing required envelope field");
        }
        bool rejected = false;
        try { ValidateEnvelope(envelope, L"different"); } catch (...) { rejected = true; }
        Check(rejected, "response identity mismatch");
        for (const auto* remote : {L"ACCESS_CODE_REQUIRED", L"ACCESS_CODE_INVALID", L"ACCESS_CODE_RATE_LIMITED",
            L"TWO_FACTOR_REQUIRED", L"TWO_FACTOR_CODE_INVALID", L"TWO_FACTOR_SETUP_REQUIRED"}) {
            auto failed = JsonObject::Parse(envelope.Stringify());
            failed.Insert(L"ok", JsonValue::CreateBooleanValue(false)); failed.Remove(L"result");
            auto error = JsonObject::Parse(L"{\"code\":\"FAILED_PRECONDITION\",\"message\":\"challenge\",\"retryable\":false,\"futureField\":true}");
            error.Insert(L"remoteCode", JsonValue::CreateStringValue(remote)); failed.Insert(L"error", error);
            bool preserved = false;
            try { ValidateEnvelope(failed, L"request"); }
            catch (const IpcFailure& failure) { preserved = failure.remoteCode == remote && !failure.message().empty(); }
            Check(preserved, "structured credential challenge survives IPC validation");
        }
        auto native = NativeAuthorizationParameters(L"demo", L"user", L"password", L"pending", L"access", L"012345", true);
        Check(JsonString(native, L"accessCode") == L"access" && JsonString(native, L"twofaCode") == L"012345" &&
            JsonBool(native, L"trustDevice") && JsonString(native, L"requestId") == L"pending", "native credentials and OTP leading zero preserved");
        native = NativeAuthorizationParameters(L"demo", L"user", L"password", L"pending", L"", L"", true);
        Check(!native.HasKey(L"accessCode") && !native.HasKey(L"twofaCode") && !native.HasKey(L"trustDevice"), "unused factors omitted for ordinary login");
        CheckPipeRoundTrip();

        // Real window/message handling with no daemon, credentials, network or registry writes.
        WNDCLASSW klass{}; klass.lpfnWndProc = WindowProc; klass.hInstance = GetModuleHandleW(nullptr); klass.lpszClassName = kWindowClass;
        RegisterClassW(&klass);
        auto window = CreateWindowW(kWindowClass, L"FnCPN native regression", WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN, 0, 0, 740, 760, nullptr, nullptr, klass.hInstance, nullptr);
        if (!window) throw std::runtime_error("window creation");
        auto systemSend=&::SendMessageW;
        systemSend(window,kStatusReady,static_cast<WPARAM>(0xdeadbeef),0);
        Check(g_postedPayloads.empty(), "forged result cannot dereference an arbitrary pointer");
        int disposed=0;
        auto originalWindow=g_window; g_window=reinterpret_cast<HWND>(static_cast<INT_PTR>(-2));
        Check(!PostPayload(WM_APP+100,new LifetimeProbe{&disposed}), "failed post rejected");
        g_window=originalWindow;
        Check(disposed==1 && g_postedPayloads.empty(), "failed post releases payload");
        auto numeric=JsonObject::Parse(L"{\"large\":1e30,\"fraction\":1.5}");
        Check(JsonInt(numeric,L"large",7)==7 && JsonInt(numeric,L"fraction",7)==7, "numeric conversion is bounded and integral");
        KillTimer(window, kRefreshTimer);
        if (argc > 1) {
            g_configured = false; g_viewLogin = true; g_busy = false; Layout(window);
            if (std::string(argv[1]) == "--preview-details") {
                auto* demo = new RefreshResult{};
                demo->fnId = L"rectcircle-mi-nuc"; demo->state = L"DIRECT"; demo->label = L("IPv6 直连");
                demo->path = L"IPv6 / UDP"; demo->address = L"10.253.203.3/32";
                demo->network = L"FnCPN · MTU 1280"; demo->handshake = L"14s";
                demo->privilege = L("活动"); demo->privilegedAvailable = true; demo->administrator = true;
                demo->direct = L("直接连接已建立"); demo->nas = L"192.168.71.2";
                demo->details = L"UDP [2001:db8::1]:54789\r\nIPv6 candidates: 2, attempts: 1";
                SendMessageW(window, kStatusReady, reinterpret_cast<WPARAM>(demo), 0);
            }
            SetWindowPos(window, nullptr, 0, 0, Scale(window, 620), Scale(window, 744), SWP_NOMOVE | SWP_NOZORDER);
            ShowWindow(window, SW_SHOW); ShowWindow(window, SW_SHOWNORMAL);
            MSG message{};
            while (GetMessageW(&message, nullptr, 0, 0) > 0) {
                if (!IsDialogMessageW(window, &message)) { TranslateMessage(&message); DispatchMessageW(&message); }
            }
            return 0;
        }
        g_configured = true; g_loginRequested = true; g_viewLogin = true;
        SetWindowTextW(g_fnIdInput, L"new-target");
        auto* snapshot = new RefreshResult{}; snapshot->fnId = L"old-target"; snapshot->state = L"PAUSED";
        SendMessageW(window, kStatusReady, reinterpret_cast<WPARAM>(snapshot), 0);
        Check(g_viewLogin && GetText(g_fnIdInput) == L"new-target", "refresh preserves explicit target edit");
        Check(!(GetWindowLongW(g_connect, GWL_STYLE) & WS_VISIBLE), "details hidden during login");
        g_loginInFlight = true; g_loginGeneration = 2;
        SendMessageW(window, kLoginResult, reinterpret_cast<WPARAM>(new LoginResult{L"stale error", L""}), 1);
        Check(g_loginInFlight && GetText(g_loginError) != L"stale error", "stale login result ignored");
        SendMessageW(window, kLoginResult, reinterpret_cast<WPARAM>(new LoginResult{L"current error", L""}), 2);
        Check(!g_loginInFlight && GetText(g_loginError) == L"current error", "current failure retained");
        SetWindowTextW(g_passwordInput, L"retained-password");
        g_authorizationRequest = L"pending"; g_loginInFlight = true;
        SendMessageW(window, kLoginResult, reinterpret_cast<WPARAM>(new LoginResult{L"access required", L"ACCESS_CODE_REQUIRED"}), 2);
        Check(g_accessCodeRequired && !g_otpRequired && g_authorizationRequest == L"pending" &&
            GetText(g_passwordInput) == L"retained-password", "access challenge retains password and authorization");
        Check((GetWindowLongW(g_accessCodeInput, GWL_STYLE) & WS_VISIBLE) != 0, "access input visible on challenge");
        SetWindowTextW(g_accessCodeInput, L"access-secret");
        g_loginInFlight = true;
        SendMessageW(window, kLoginResult, reinterpret_cast<WPARAM>(new LoginResult{L"OTP required", L"TWO_FACTOR_REQUIRED"}), 2);
        Check(!g_accessCodeRequired && g_otpRequired && GetText(g_accessCodeInput).empty() &&
            g_authorizationRequest == L"pending", "OTP stage reuses gateway session and authorization");
        Check((GetWindowLongW(g_otpInput, GWL_STYLE) & WS_VISIBLE) &&
            !(GetWindowLongW(g_accessCodeInput, GWL_STYLE) & WS_VISIBLE), "only current factor is visible");
        SetWindowTextW(g_otpInput, L"123456"); SendMessageW(g_trustDevice, BM_SETCHECK, BST_CHECKED, 0);
        g_loginInFlight = true;
        SendMessageW(window, kLoginResult, reinterpret_cast<WPARAM>(new LoginResult{L"wrong OTP", L"TWO_FACTOR_CODE_INVALID"}), 2);
        Check(GetText(g_otpInput).empty() && GetText(g_passwordInput) == L"retained-password" &&
            SendMessageW(g_trustDevice, BM_GETCHECK, 0, 0) == BST_CHECKED, "OTP rejection clears only the OTP");
        const auto probeGeneration = g_probeGeneration;
        SetWindowTextW(g_fnIdInput, L"another-target");
        Check(!g_otpRequired && !g_accessCodeRequired && g_authorizationRequest.empty() &&
            GetText(g_passwordInput).empty(), "edit notifications clear old NAS credentials and pending request");
        SendMessageW(window, kAccessProbeReady, reinterpret_cast<WPARAM>(new AccessProbeResult{L"new-target", probeGeneration, true, true}), 0);
        Check(!g_accessCodeRequired, "stale target probe ignored");
        SendMessageW(window, kAccessProbeReady, reinterpret_cast<WPARAM>(new AccessProbeResult{L"another-target", g_probeGeneration, true, true}), 0);
        Check(g_accessCodeRequired, "current target probe reveals access field");
        // A CLI-owned request must survive the programmatic target change.
        HandleActivationArguments({L"fncpn://authorize/cli-target?request=cli-request"});
        Check(g_authorizationRequest == L"cli-request" && g_externalAuthorization, "CLI activation preserves its authorization request");
        SendMessageW(window, WM_COMMAND, kLoginCancel, 0);
        Check(!g_externalAuthorization && g_authorizationRequest.empty() && !g_accessCodeRequired && !g_otpRequired,
            "cancel clears pending authorization and factors");
        EnableWindow(g_retry, TRUE);
        Check(SendMessageW(g_retry, WM_SETCURSOR, reinterpret_cast<WPARAM>(g_retry), MAKELPARAM(HTCLIENT, WM_MOUSEMOVE)) == TRUE &&
            GetCursor() == LoadCursorW(nullptr, IDC_HAND), "enabled button uses hand cursor");
        g_viewLogin = false; Layout(window);
        // DNS follows handshake, and the copy icon owns a separate clickable
        // rectangle even when a long suffix must be ellipsized.
        auto controlRect = [window](HWND control) {
            RECT rect{}; GetWindowRect(control, &rect);
            MapWindowPoints(nullptr, window, reinterpret_cast<POINT*>(&rect), 2);
            return rect;
        };
        for (const auto& suffix : {std::wstring(L"home.fncpn"), std::wstring(180, L'w') + L".fncpn"}) {
            g_dnsSuffix = suffix;
            SetWindowTextW(g_dnsValue, (L"Ready · " + suffix).c_str());
            Layout(window);
            const auto dns = controlRect(g_dnsValue), copy = controlRect(g_copyDNSSuffix);
            const auto handshake = controlRect(g_values[3]), privileged = controlRect(g_values[4]);
            Check(handshake.bottom <= dns.top && dns.bottom <= privileged.top, "DNS row matches macOS order");
            Check(dns.right < copy.left && dns.top == copy.top, "DNS text does not cover copy icon");
            RECT client{}; GetClientRect(window, &client);
            Check(copy.right <= client.right, "long DNS suffix keeps copy icon in bounds");
            POINT center{(copy.left + copy.right) / 2, (copy.top + copy.bottom) / 2};
            Check(ChildWindowFromPointEx(window, center, CWP_SKIPINVISIBLE | CWP_SKIPDISABLED) == g_copyDNSSuffix,
                "DNS copy icon receives pointer hit testing");
            Check((GetWindowLongW(g_copyDNSSuffix, GWL_STYLE) & WS_VISIBLE) && IsWindowEnabled(g_copyDNSSuffix) && g_dnsButtonIcon,
                "DNS copy button is visible, enabled and has an icon");
        }
        g_dnsSuffix.clear(); Layout(window);
        Check(!IsWindowEnabled(g_copyDNSSuffix), "empty DNS suffix disables copy");
        SetWindowTextW(g_diagnosticValue, L"Short diagnostic"); UpdateDiagnosticScrollBar();
        Check(!(GetWindowLongW(g_diagnosticValue, GWL_STYLE) & WS_VSCROLL), "short diagnostic hides scrollbar");
        std::wstring longDiagnostic;
        for (int i = 0; i < 40; ++i) longDiagnostic += L"Diagnostic line\r\n";
        SetWindowTextW(g_diagnosticValue, longDiagnostic.c_str()); UpdateDiagnosticScrollBar();
        Check(GetWindowLongW(g_diagnosticValue, GWL_STYLE) & WS_VSCROLL, "overflow diagnostic shows scrollbar");
        SetWindowTextW(g_diagnosticValue, L"Short again"); UpdateDiagnosticScrollBar();
        Check(!(GetWindowLongW(g_diagnosticValue, GWL_STYLE) & WS_VSCROLL), "shrinking diagnostic hides scrollbar again");
        g_fnId = L"existing-nas";
        SendMessageW(window,WM_COMMAND,kRelogin,0);
        Check(GetText(g_fnIdInput) == L"existing-nas", "relogin retains FN Connect ID");
        Check(GetText(g_identity) == L("重新登录"), "login heading does not duplicate FN ID");
        Check((GetWindowLongW(g_autostart,GWL_STYLE) & BS_TYPEMASK) == BS_AUTOCHECKBOX &&
            !(GetWindowLongW(g_autostart,GWL_STYLE) & BS_PUSHLIKE), "autostart is a checkbox");
        g_loginRequested=false; g_viewLogin=false; g_operationError=L"previous failure";
        auto* connected = new RefreshResult{}; connected->fnId=L"existing-nas"; connected->state=L"DIRECT";
        connected->privilegedAvailable=true; connected->statusError=L"previous daemon failure";
        SendMessageW(window,kStatusReady,reinterpret_cast<WPARAM>(connected),0);
        Check(GetText(g_statusError).empty(), "connected snapshot clears stale error banner");
        g_username=L"test-user";
        SetStatus(L"Connected",L"FnCPN · existing-nas");
        Check(GetText(g_identity)==L"existing-nas · test-user", "details identity includes FN ID and username");
        SendMessageW(window,kAdminReady,reinterpret_cast<WPARAM>(new std::pair<std::wstring,std::wstring>(L"",L"Management probe failed")),0);
        g_diagnosticBase=L("无异常"); Layout(window);
        Check(GetText(g_diagnosticValue).find(L("无异常"))==std::wstring::npos, "error excludes no-anomalies placeholder");
        Check(GetText(g_diagnosticValue).find(L"Management probe failed")!=std::wstring::npos,
            "management errors appear in diagnostics");
        Check(!(GetWindowLongW(g_statusError,GWL_STYLE)&WS_VISIBLE), "separate error banner remains hidden");
        Check(!(GetWindowLongW(g_fnIdInput,GWL_STYLE)&WS_BORDER), "inner editor has no duplicate border");
        Check(GetWindowLongW(static_cast<HWND>(GetPropW(g_fnIdInput,L"InputFrame")),GWL_STYLE)&WS_BORDER, "outer input frame owns the border");
        SetWindowPos(window,nullptr,0,0,Scale(window,620),Scale(window,660),SWP_NOMOVE|SWP_NOZORDER);
        RECT checkbox{},client{}; GetWindowRect(g_autostart,&checkbox); MapWindowPoints(nullptr,window,reinterpret_cast<POINT*>(&checkbox),2);
        GetClientRect(window,&client);
        Check(checkbox.bottom <= client.bottom, "adaptive layout keeps autostart visible");
        SendMessageW(window, WM_CLOSE, 0, 0);
        Check(IsWindow(window) && !IsWindowVisible(window), "close keeps tray lifecycle");
        Check(SendMessageW(window, WM_QUERYENDSESSION, 0, ENDSESSION_CLOSEAPP) == TRUE, "installer shutdown accepted");
        SendMessageW(window, WM_ENDSESSION, FALSE, ENDSESSION_CLOSEAPP);
        Check(IsWindow(window) && !g_exitStarted, "cancelled session preserves UI");
        UpdateFont(window);
        const auto gdiBefore=GetGuiResources(GetCurrentProcess(),GR_GDIOBJECTS);
        for(int i=0;i<100;++i) UpdateFont(window);
        const auto gdiAfter=GetGuiResources(GetCurrentProcess(),GR_GDIOBJECTS);
        Check(gdiAfter<=gdiBefore+2, "repeated font and icon updates do not grow GDI resources");
        int pendingDisposed=0;
        Check(PostPayload(WM_APP+100,new LifetimeProbe{&pendingDisposed}), "pending result queued");
        std::atomic_int completed=0;
        for(int i=0;i<8;++i) StartWorker([&completed]{++completed;});
        SendMessageW(window, WM_ENDSESSION, TRUE, ENDSESSION_CLOSEAPP);
        Check(!IsWindow(window) && g_closing && g_exitStarted, "installer shutdown destroys hidden UI synchronously");
        JoinWorkers();
        Check(pendingDisposed==1 && g_postedPayloads.empty(), "shutdown drains pending results");
        Check(completed==8 && g_workers.empty(), "shutdown joins owned workers");
        Check(!PostPayload(WM_APP+100,new LifetimeProbe{&pendingDisposed}) && pendingDisposed==2, "late result is released after shutdown");
        std::cout << "Native UI protocol, activation, navigation and window regressions passed\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n'; ClosePayloadQueue(); JoinWorkers(); return 1;
    } catch (const winrt::hresult_error& error) {
        std::wcerr << error.message().c_str() << '\n'; ClosePayloadQueue(); JoinWorkers(); return 1;
    }
}
