#include <windows.h>
#include <commctrl.h>
#include <uxtheme.h>
#include <vsstyle.h>
#include <shellapi.h>
#include <shlobj.h>
#include <objbase.h>
#include <sddl.h>
#include <shellapi.h>
#include <shellscalingapi.h>
#include <shlwapi.h>
#include <wrl.h>
#include <WebView2.h>
#include <winrt/base.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Data.Json.h>
#include <algorithm>
#include <array>
#include <cwctype>
#include <memory>
#include <string>
#include <string_view>
#include <thread>
#include <vector>
#include <cstdint>
#include <atomic>
#include <functional>
#include <unordered_set>
#include <unordered_map>
#include <mutex>
#include <cmath>
#include <limits>
#include <winhttp.h>
#include <winreg.h>
#include "Localization.h"

using namespace winrt::Windows::Data::Json;

namespace {
constexpr wchar_t kWindowClass[] = L"FnCPN.NativeWindow";
constexpr wchar_t kAdminWindowClass[] = L"FnCPN.AdminWindow";
constexpr int kIpcVersion = 5;
constexpr UINT_PTR kRefreshTimer = 1;
constexpr UINT kStatusReady = WM_APP + 1;
constexpr UINT kDaemonReady = WM_APP + 2;
constexpr UINT kLoginResult = WM_APP + 3;
constexpr UINT kTrayCallback = WM_APP + 4;
constexpr UINT kExitReady = WM_APP + 5;
constexpr UINT kActivateWindow = WM_APP + 6;
constexpr UINT kActivationReceived = WM_APP + 7;
constexpr UINT kAdminReady = WM_APP + 8;
constexpr UINT kOperationReady = WM_APP + 9;
constexpr UINT kAuthorizationStarted = WM_APP + 10;
constexpr UINT kStatusChanged = WM_APP + 11;
constexpr UINT kWorkerFailed = WM_APP + 12;
constexpr UINT_PTR kStatusWatchTimer = 2;
constexpr int kState = 1001;
constexpr int kIdentity = 1002;
constexpr int kConnect = 1003;
constexpr int kRefresh = 1004;
constexpr int kExit = 1005;
constexpr int kFnId = 1006;
constexpr int kUsername = 1007;
constexpr int kPassword = 1008;
constexpr int kLogin = 1009;
constexpr int kLoginError = 1010;
constexpr int kFnIdLabel = 1011;
constexpr int kUsernameLabel = 1012;
constexpr int kPasswordLabel = 1013;
constexpr int kRetry = 1014;
constexpr int kRelogin = 1015;
constexpr int kChangeServer = 1016;
constexpr int kAdmin = 1017;
constexpr int kCopyDiagnostics = 1018;
constexpr int kOpenLog = 1019;
constexpr int kOpenNas = 1020;
constexpr int kAutostart = 1021;
constexpr int kRepair = 1022;
constexpr int kPathValue = 1023;
constexpr int kAddressValue = 1024;
constexpr int kNetworkValue = 1025;
constexpr int kHandshakeValue = 1026;
constexpr int kPrivilegeValue = 1027;
constexpr int kDirectValue = 1028;
constexpr int kNasValue = 1029;
constexpr int kDiagnosticValue = 1030;
constexpr int kStatusError = 1031;
constexpr int kLoginReturn = 1032;
constexpr int kLoginCancel = 1033;
constexpr int kCopyDNSSuffix = 1034;

HWND g_window{};
HWND g_state{};
HWND g_identity{};
HWND g_connect{};
HWND g_fnIdInput{};
HWND g_usernameInput{};
HWND g_passwordInput{};
HWND g_loginError{};
HWND g_loginButton{};
HWND g_fnIdLabel{};
HWND g_usernameLabel{};
HWND g_passwordLabel{};
HWND g_retry{};
HWND g_relogin{};
HWND g_changeServer{};
HWND g_admin{};
HWND g_copyDiagnostics{};
HWND g_openLog{};
HWND g_openNas{};
HWND g_autostart{};
HWND g_repair{};
HWND g_values[7]{};
HWND g_fieldLabels[7]{};
HWND g_diagnosticValue{};
HWND g_dnsLabel{}, g_dnsValue{}, g_copyDNSSuffix{};
std::wstring g_dnsSuffix;
HWND g_statusError{};
HWND g_loginReturn{};
HWND g_loginCancel{};
HANDLE g_singleInstance{};
std::wstring g_daemonPath;
std::wstring g_fnId;
std::wstring g_stateName = L("正在连接客户端服务…");
bool g_busy{};
bool g_configured{};
bool g_viewLogin{};
bool g_localWaiting{};
bool g_allowExit{};
NOTIFYICONDATAW g_tray{};
bool g_trayAdded{};
UINT g_taskbarCreated{};
std::wstring g_remoteState;
std::wstring g_username;
std::wstring g_authorizationRequest;
std::wstring g_adminOrigin;
std::wstring g_adminUrl;
std::wstring g_operationError;
std::wstring g_pendingLoginFailure;
JsonObject g_diagnostics{nullptr};
Microsoft::WRL::ComPtr<ICoreWebView2Environment> g_webEnvironment;
Microsoft::WRL::ComPtr<ICoreWebView2Controller> g_webController;
Microsoft::WRL::ComPtr<ICoreWebView2> g_webView;
Microsoft::WRL::ComPtr<ICoreWebView2_2> g_webView2;
HWND g_adminWindow{};
bool g_externalAuthorization{};
bool g_loginRequested{};
bool g_autoStart{};
bool g_operationInFlight{};
bool g_loginInFlight{};
bool g_adminOpening{};
uint64_t g_adminGeneration{};
bool g_startInBackground{};
bool g_exitStarted{};
bool g_privilegedPrompted{};
bool g_privilegedAvailable{};
bool g_administrator{};
bool g_watchStarted{};
std::atomic_bool g_closing{};
unsigned g_loginGeneration{};
HFONT g_font{};
HFONT g_titleFont{}, g_deviceFont{}, g_statusFont{}, g_monoFont{};
HWND g_brand{}, g_versionLabel{}, g_headerIcon{}, g_diagnosticsLabel{};
HBRUSH g_backgroundBrush{};
COLORREF g_backgroundColor = RGB(245, 246, 248);
bool g_highContrast{};
HICON g_headerImage{};
int g_scrollY{};
ULONGLONG g_statusGeneration{};
EventRegistrationToken g_navigationToken{};
EventRegistrationToken g_newWindowToken{};
EventRegistrationToken g_responseToken{};

struct RefreshResult {
    std::wstring label;
    std::wstring fnId;
    std::wstring state;
    bool localWaiting{};
    std::wstring username;
    std::wstring path;
    std::wstring address;
    std::wstring nas;
    std::wstring network;
    std::wstring handshake;
    std::wstring privilege;
    std::wstring direct;
    std::wstring details;
    std::wstring dnsText, dnsSuffix;
    std::wstring statusError;
    std::wstring diagnosticJson;
    bool administrator{};
    bool privilegedAvailable{};
};

struct PostedPayload { UINT message; void* pointer; std::function<void()> destroy; };
std::mutex g_payloadMutex;
std::unordered_map<WPARAM,PostedPayload> g_postedPayloads;
WPARAM g_nextPayloadKey=1;

template<class T> WPARAM RegisterPayload(UINT message,T* pointer) {
    std::lock_guard lock(g_payloadMutex);
    const auto key=g_nextPayloadKey++;
    g_postedPayloads.emplace(key,PostedPayload{message,pointer,[pointer]{delete pointer;}});
    return key;
}
template<class T> bool PostPayload(UINT message,T* pointer,LPARAM parameter=0) {
    std::unique_ptr<T> owner(pointer);
    std::lock_guard lock(g_payloadMutex);
    if(g_closing) return false;
    const auto key=g_nextPayloadKey++;
    g_postedPayloads.emplace(key,PostedPayload{message,pointer,[pointer]{delete pointer;}});
    if(!PostMessageW(g_window,message,key,parameter)) { g_postedPayloads.erase(key); return false; }
    owner.release(); return true;
}
template<class T> std::unique_ptr<T> TakePayload(UINT message,WPARAM key) {
    std::lock_guard lock(g_payloadMutex);
    auto entry=g_postedPayloads.find(key);
    if(entry==g_postedPayloads.end() || entry->second.message!=message) return {};
    auto* pointer=static_cast<T*>(entry->second.pointer);
    g_postedPayloads.erase(entry);
    return std::unique_ptr<T>(pointer);
}
void ClosePayloadQueue() {
    std::lock_guard lock(g_payloadMutex);
    g_closing=true;
    for(auto& [key,payload] : g_postedPayloads) payload.destroy();
    g_postedPayloads.clear();
}

std::mutex g_pipeMutex;
std::unordered_set<HANDLE> g_activePipes;
struct PipeGuard {
    HANDLE value;
    explicit PipeGuard(HANDLE pipe):value(pipe) { std::lock_guard lock(g_pipeMutex); if(g_closing) { CloseHandle(value); value=INVALID_HANDLE_VALUE; throw winrt::hresult_error(HRESULT_FROM_WIN32(ERROR_CANCELLED)); } try { g_activePipes.insert(value); } catch(...) { CloseHandle(value); throw; } }
    PipeGuard(const PipeGuard&)=delete;
    PipeGuard& operator=(const PipeGuard&)=delete;
    ~PipeGuard() { std::lock_guard lock(g_pipeMutex); g_activePipes.erase(value); CloseHandle(value); }
};
void CancelWorkerIO() { std::lock_guard lock(g_pipeMutex); for(auto pipe:g_activePipes) CancelIoEx(pipe,nullptr); }
struct Worker { std::thread thread; std::shared_ptr<std::atomic_bool> done; };
std::mutex g_workerMutex;
std::vector<Worker> g_workers;
template<class F,class... Args> void StartWorker(F&& function,Args&&... arguments) {
    std::lock_guard lock(g_workerMutex);
    if(g_closing) return;
    for(auto it=g_workers.begin();it!=g_workers.end();) {
        if(*it->done) { it->thread.join(); it=g_workers.erase(it); } else ++it;
    }
    auto done=std::make_shared<std::atomic_bool>(false);
    auto task=std::bind(std::forward<F>(function),std::forward<Args>(arguments)...);
    g_workers.reserve(g_workers.size()+1);
    g_workers.push_back({std::thread([task=std::move(task),done]() mutable { try {task();} catch(...) {
        std::lock_guard lock(g_payloadMutex); if(!g_closing) PostMessageW(g_window,kWorkerFailed,0,0);
    } *done=true; }),done});
}
void JoinWorkers() {
    CancelWorkerIO();
    std::vector<Worker> workers;
    {std::lock_guard lock(g_workerMutex); workers.swap(g_workers);}
    for(auto& worker:workers) worker.thread.join();
}

std::wstring GetText(HWND control) {
    const int length = GetWindowTextLengthW(control);
    std::wstring value(static_cast<size_t>(length) + 1, L'\0');
    if (length) GetWindowTextW(control, value.data(), length + 1);
    value.resize(static_cast<size_t>(length));
    return value;
}

std::wstring JsonString(const JsonObject& object, const wchar_t* name, const wchar_t* fallback = L"") {
    if (!object.HasKey(name)) return fallback;
    auto value = object.GetNamedValue(name);
    if (value.ValueType() != JsonValueType::String) return fallback;
    return value.GetString().c_str();
}

bool JsonBool(const JsonObject& object, const wchar_t* name, bool fallback = false) {
    if (!object.HasKey(name) || object.GetNamedValue(name).ValueType() != JsonValueType::Boolean) return fallback;
    return object.GetNamedBoolean(name);
}

int JsonInt(const JsonObject& object, const wchar_t* name, int fallback = 0) {
    if (!object.HasKey(name) || object.GetNamedValue(name).ValueType() != JsonValueType::Number) return fallback;
    const double value=object.GetNamedNumber(name);
    if(!std::isfinite(value) || value<std::numeric_limits<int>::min() || value>std::numeric_limits<int>::max() || std::floor(value)!=value) return fallback;
    return static_cast<int>(value);
}

std::wstring FailureMessage(const JsonObject& failure) {
    if (failure.Size() == 0) return {};
    const auto code = JsonString(failure, L"code");
    const auto operation = JsonString(failure, L"operation");
    if (code == L"AUTH_REQUIRED") return operation.find(L"native_session.authenticate") != std::wstring::npos ? L("用户名或密码无效，请重试") : L("登录已失效，请重新输入密码");
    if (code == L"FAILED_PRECONDITION" && JsonString(failure, L"remoteCode") == L"TWO_FACTOR_REQUIRED") return L("该账号需要两步验证，当前版本暂不支持。");
    if (code == L"PERMISSION_DENIED") return L("权限不足，无法完成操作。");
    if (code == L"SERVER_UNAVAILABLE") return L("未找到 FnCPN 服务，请在 fnOS 安装或启动 FnCPN 后重新连接。");
    if (code == L"DEVICE_REVOKED") return L("设备授权已撤销，请重新登录。");
    if (code == L"TIMEOUT") return L("操作超时，请重试。");
    if (code == L"VERSION_INCOMPATIBLE") {
        const auto summary = JsonString(failure, L"upgradeTarget") == L"client" ? L("客户端版本不兼容，请升级客户端。") : L("服务端版本不兼容，请升级服务端。");
        return std::wstring(summary) + L" (client " + JsonString(failure, L"clientVersion", FNCPN_PRODUCT_VERSION) + L", server " + JsonString(failure, L"serverVersion", L"?") + L")";
    }
    return JsonString(failure, L"message");
}

uint32_t ReadU32(const std::array<uint8_t, 4>& bytes) {
    return (uint32_t(bytes[0]) << 24) | (uint32_t(bytes[1]) << 16) | (uint32_t(bytes[2]) << 8) | uint32_t(bytes[3]);
}

void WriteU32(std::array<uint8_t, 4>& bytes, uint32_t value) {
    bytes = {uint8_t(value >> 24), uint8_t(value >> 16), uint8_t(value >> 8), uint8_t(value)};
}

DWORD TimeoutForMethod(const wchar_t* method) {
    if (wcscmp(method, L"authorize-native") == 0 || wcscmp(method, L"watch-client-status") == 0 ||
        wcscmp(method, L"authorization-wait") == 0) return 310000;
    if (wcscmp(method, L"connect") == 0 || wcscmp(method, L"retry") == 0) return 45000;
    return 30000;
}

bool TransferExact(HANDLE pipe, void* data, DWORD size, bool write, DWORD timeout) {
    auto* bytes = static_cast<uint8_t*>(data);
    DWORD offset = 0;
    const auto deadline = GetTickCount64() + timeout;
    while (offset < size) {
        if(g_closing) { SetLastError(ERROR_CANCELLED); return false; }
        OVERLAPPED overlapped{};
        overlapped.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if(!overlapped.hEvent) return false;
        if (!overlapped.hEvent) return false;
        DWORD transferred = 0;
        const BOOL started = write
            ? WriteFile(pipe, bytes + offset, size - offset, &transferred, &overlapped)
            : ReadFile(pipe, bytes + offset, size - offset, &transferred, &overlapped);
        if (!started && GetLastError() != ERROR_IO_PENDING) { CloseHandle(overlapped.hEvent); return false; }
        if (!started) {
            const auto now = GetTickCount64();
            const auto wait = WaitForSingleObject(overlapped.hEvent, now >= deadline ? 0 : static_cast<DWORD>(deadline - now));
            if (wait != WAIT_OBJECT_0) {
                CancelIoEx(pipe, &overlapped);
                WaitForSingleObject(overlapped.hEvent, INFINITE);
                CloseHandle(overlapped.hEvent);
                SetLastError(wait == WAIT_TIMEOUT ? ERROR_TIMEOUT : GetLastError());
                return false;
            }
            if (!GetOverlappedResult(pipe, &overlapped, &transferred, FALSE)) { CloseHandle(overlapped.hEvent); return false; }
        }
        CloseHandle(overlapped.hEvent);
        if (!transferred) return false;
        offset += transferred;
    }
    return true;
}

bool KnownErrorCode(const std::wstring& code) {
    static const std::unordered_set<std::wstring> known{
        L"INVALID_ARGUMENT", L"AUTH_REQUIRED", L"PERMISSION_DENIED", L"NOT_FOUND", L"ALREADY_EXISTS",
        L"FAILED_PRECONDITION", L"DEVICE_REVOKED", L"UNAVAILABLE", L"CONFLICT", L"TIMEOUT", L"CANCELED",
        L"INTERNAL", L"PROTOCOL_ERROR", L"SERVER_UNAVAILABLE", L"VERSION_INCOMPATIBLE", L"RESOURCE_EXHAUSTED", L"DISCOVERY_FAILED"
    };
    return known.contains(code);
}

void ValidateEnvelope(const JsonObject& root, const std::wstring& id) {
    if (!root.HasKey(L"id") || root.GetNamedValue(L"id").ValueType() != JsonValueType::String || root.GetNamedString(L"id") != id ||
        !root.HasKey(L"version") || root.GetNamedNumber(L"version") != kIpcVersion ||
        !root.HasKey(L"productVersion") || root.GetNamedValue(L"productVersion").ValueType() != JsonValueType::String || root.GetNamedString(L"productVersion").empty() ||
        root.GetNamedString(L"productVersion") != FNCPN_PRODUCT_VERSION)
        throw winrt::hresult_error(E_INVALIDARG, L("本地组件版本不一致，请完成更新并重新启动 FnCPN。"));
    if (!root.HasKey(L"ok") || (root.GetNamedValue(L"ok").ValueType() != JsonValueType::Boolean)) throw winrt::hresult_error(E_INVALIDARG);
    if (!root.GetNamedBoolean(L"ok")) {
        if (root.HasKey(L"result") || !root.HasKey(L"error") || root.GetNamedValue(L"error").ValueType() != JsonValueType::Object)
            throw winrt::hresult_error(E_INVALIDARG);
        const auto error = root.GetNamedObject(L"error");
        if (!error.HasKey(L"code") || !error.HasKey(L"message") ||
            error.GetNamedValue(L"code").ValueType() != JsonValueType::String ||
            error.GetNamedValue(L"message").ValueType() != JsonValueType::String ||
            !error.HasKey(L"retryable") || error.GetNamedValue(L"retryable").ValueType() != JsonValueType::Boolean ||
            !KnownErrorCode(JsonString(error, L"code"))) throw winrt::hresult_error(E_INVALIDARG);
        const auto message = FailureMessage(error);
        throw winrt::hresult_error(E_FAIL, (message.empty() ? error.GetNamedString(L"message", L("FnCPN 请求失败")) : winrt::hstring(message)).c_str());
    }
    if (!root.HasKey(L"result") || root.GetNamedValue(L"result").ValueType() != JsonValueType::Object || (root.HasKey(L"error") && root.GetNamedValue(L"error").ValueType() != JsonValueType::Null)) throw winrt::hresult_error(E_INVALIDARG);
}

bool IsSameUserSessionServer(HANDLE pipe) {
    ULONG pid = 0; DWORD expectedSession = 0, actualSession = 0;
    if (!GetNamedPipeServerProcessId(pipe, &pid) || !ProcessIdToSessionId(GetCurrentProcessId(), &expectedSession) ||
        !ProcessIdToSessionId(pid, &actualSession) || expectedSession != actualSession) return false;
    HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
    if (!process) return false;
    HANDLE ownToken{}, peerToken{};
    bool equal = false;
    if (OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &ownToken) && OpenProcessToken(process, TOKEN_QUERY, &peerToken)) {
        DWORD ownSize = 0, peerSize = 0;
        GetTokenInformation(ownToken, TokenUser, nullptr, 0, &ownSize);
        GetTokenInformation(peerToken, TokenUser, nullptr, 0, &peerSize);
        std::vector<BYTE> own(ownSize), peer(peerSize);
        if (ownSize && peerSize && GetTokenInformation(ownToken, TokenUser, own.data(), ownSize, &ownSize) &&
            GetTokenInformation(peerToken, TokenUser, peer.data(), peerSize, &peerSize))
            equal = EqualSid(reinterpret_cast<TOKEN_USER*>(own.data())->User.Sid, reinterpret_cast<TOKEN_USER*>(peer.data())->User.Sid) != FALSE;
    }
    if (ownToken) CloseHandle(ownToken);
    if (peerToken) CloseHandle(peerToken);
    CloseHandle(process);
    return equal;
}

JsonObject CallIpc(const wchar_t* method, const JsonObject* parameters = nullptr) {
    DWORD sessionId = 0;
    ProcessIdToSessionId(GetCurrentProcessId(), &sessionId);
    const std::wstring pipeName = L"\\\\.\\pipe\\FnCPN.Client.v1." + std::to_wstring(sessionId)
#ifdef FNCPN_NATIVE_TEST
        + L".native-test." + std::to_wstring(GetCurrentProcessId())
#endif
        ;
    if (!WaitNamedPipeW(pipeName.c_str(), 5000)) throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    HANDLE pipe = CreateFileW(pipeName.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr, OPEN_EXISTING,
        FILE_FLAG_OVERLAPPED | SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION, nullptr);
    if (pipe == INVALID_HANDLE_VALUE) throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    PipeGuard guard{pipe};
    if (!IsSameUserSessionServer(pipe)) throw winrt::hresult_error(E_ACCESSDENIED);

    JsonObject request;
    request.Insert(L"version", JsonValue::CreateNumberValue(kIpcVersion));
    GUID requestGuid{};
    winrt::check_hresult(CoCreateGuid(&requestGuid));
    wchar_t formattedGuid[39]{};
    if (!StringFromGUID2(requestGuid, formattedGuid, static_cast<int>(std::size(formattedGuid))))
        throw winrt::hresult_error(E_FAIL);
    std::wstring id;
    for (const wchar_t ch : formattedGuid) if (ch && ch != L'{' && ch != L'}' && ch != L'-') id.push_back(ch);
    request.Insert(L"id", JsonValue::CreateStringValue(id));
    request.Insert(L"method", JsonValue::CreateStringValue(method));
    request.Insert(L"params", parameters ? parameters->as<IJsonValue>() : JsonValue::CreateNullValue());
    const auto payloadWide = request.Stringify();
    const auto payloadSize = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, payloadWide.c_str(), -1, nullptr, 0, nullptr, nullptr);
    if (payloadSize <= 1 || payloadSize > 262145) throw winrt::hresult_error(E_INVALIDARG);
    std::vector<uint8_t> payload(static_cast<size_t>(payloadSize));
    WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, payloadWide.c_str(), -1, reinterpret_cast<char*>(payload.data()), payloadSize, nullptr, nullptr);
    payload.resize(static_cast<size_t>(payloadSize - 1));
    std::array<uint8_t, 4> header{};
    WriteU32(header, static_cast<uint32_t>(payload.size()));
    const auto timeout = TimeoutForMethod(method);
    if (!TransferExact(pipe, header.data(), static_cast<DWORD>(header.size()), true, timeout) ||
        !TransferExact(pipe, payload.data(), static_cast<DWORD>(payload.size()), true, timeout))
        throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    if (!TransferExact(pipe, header.data(), static_cast<DWORD>(header.size()), false, timeout)) throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    const auto responseSize = ReadU32(header);
    if (responseSize == 0 || responseSize > 262144) throw winrt::hresult_error(E_INVALIDARG);
    std::vector<uint8_t> response(responseSize);
    if (!TransferExact(pipe, response.data(), responseSize, false, timeout)) throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    const int chars = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, reinterpret_cast<const char*>(response.data()), static_cast<int>(response.size()), nullptr, 0);
    if (chars <= 0) throw winrt::hresult_error(E_INVALIDARG);
    std::wstring responseText(static_cast<size_t>(chars), L'\0');
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, reinterpret_cast<const char*>(response.data()), static_cast<int>(response.size()), responseText.data(), chars);
    auto root = JsonObject::Parse(responseText);
    ValidateEnvelope(root, id);
    return root;
}

void SetStatus(const std::wstring& state, const std::wstring& identity = {}) {
    g_stateName = state;
    if (g_state) SetWindowTextW(g_state, state.c_str());
    if (g_identity && !identity.empty()) {
        auto device = identity.starts_with(L"FnCPN · ") ? identity.substr(8) : (identity == L"FnCPN" ? std::wstring(L("尚未登录")) : identity);
        if (identity.starts_with(L"FnCPN · ") && !g_username.empty()) device += L" · " + g_username;
        SetWindowTextW(g_identity, (g_viewLogin && g_loginRequested ? std::wstring(L("重新登录")) : device).c_str());
    }
    if (g_connect) {
        const bool connected = g_remoteState == L"LOCAL" || g_remoteState == L"DIRECT" || g_remoteState == L"RELAY" ||
            g_remoteState == L"PROBING" || g_remoteState == L"RECONNECTING";
        SetWindowTextW(g_connect, g_localWaiting ? L("继续远程连接") : (g_configured ? (connected ? L("断开连接") : L("连接")) : L("尚未登录")));
        EnableWindow(g_connect, g_configured && !g_busy);
        InvalidateRect(g_connect, nullptr, TRUE);
    }
    if (g_loginButton) EnableWindow(g_loginButton, !g_loginInFlight && !g_operationInFlight);
    if (g_trayAdded) {
        std::wstring tip = L"FnCPN · " + state;
        if (tip.size() >= std::size(g_tray.szTip)) tip.resize(std::size(g_tray.szTip) - 1);
        wcscpy_s(g_tray.szTip, tip.c_str());
        Shell_NotifyIconW(NIM_MODIFY, &g_tray);
    }
}

std::wstring LabelForState(const std::wstring& state) {
    if (state == L"LOCAL") return L("已在同一局域网");
    if (state == L"DIRECT") return L("IPv6 直连");
    if (state == L"RELAY") return L("FN Connect 中继");
    if (state == L"PROBING") return L("正在检测网络");
    if (state == L"RECONNECTING") return L("正在重新连接");
    if (state == L"AUTHORIZING") return L("正在登录");
    if (state == L"PAUSED") return L("已断开");
    if (state == L"AUTH_REQUIRED") return L("需要重新登录");
    if (state == L"SERVER_UNAVAILABLE") return L("未找到 FnCPN 服务");
    if (state == L"DAEMON_UNAVAILABLE") return L("客户端服务不可用");
    if (state == L"ERROR") return L("连接失败");
    return L("尚未连接");
}

void RefreshFromWorker() {
    winrt::init_apartment(winrt::apartment_type::multi_threaded);
    struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
    try {
        auto root = CallIpc(L"diagnose");
        auto result = root.GetNamedObject(L"result");
        auto status = result.GetNamedObject(L"status");
        const auto state = JsonString(status, L"state", L"UNCONFIGURED");
        const auto fnId = JsonString(result, L"fnId");
        const bool localWaiting = status.HasKey(L"localNetworkWaiting") && status.GetNamedBoolean(L"localNetworkWaiting");
        const auto username = JsonString(result, L"username");
        const auto path = JsonString(status, L"path");
        const auto networkInterface = JsonString(result, L"networkInterface");
        const auto mtu = JsonInt(result, L"mtu");
        const auto direct = result.GetNamedObject(L"direct", JsonObject{});
        auto refreshed = std::make_unique<RefreshResult>();
        refreshed->label = LabelForState(state);
        refreshed->fnId = fnId;
        refreshed->state = state;
        refreshed->localWaiting = localWaiting;
        refreshed->username = username;
        refreshed->path = path == L"local" ? L("局域网直达") : path == L"direct" ? L("IPv6 直连") : path == L"relay" ? L("WSS 中继") : L("未连接");
        refreshed->address = JsonString(result, L"clientAddress", L"-");
        refreshed->nas = JsonString(result, L"nasAddress", L"-");
        refreshed->network = networkInterface.empty() ? L"-" : networkInterface + (mtu > 0 ? L" · MTU " + std::to_wstring(mtu) : L"");
        refreshed->handshake = JsonString(result, L"handshakeAge", L"-");
        refreshed->privilegedAvailable = JsonBool(result, L"privilegedAvailable");
        refreshed->administrator = JsonBool(result, L"administrator");
        refreshed->privilege = !refreshed->privilegedAvailable ? L("不可用") : JsonBool(result, L"privilegedDegraded") ? L("降级") : JsonBool(result, L"privilegedActive") ? L("活动") : L("就绪");
        const auto directReason = JsonString(direct, L"reason");
        refreshed->direct = directReason == L"connected" ? L("直接连接已建立") : directReason == L"no_local_ipv6" ? L("本机网络不支持 IPv6") : directReason == L"no_server_ipv6" ? L("NAS 未提供可用的公网 IPv6") : directReason == L"server_disabled" ? L("NAS 已禁用公网 IPv6 访问") : directReason == L"handshake_failed" ? L("UDP 握手失败，已回退中继") : directReason == L"discovery_failed" ? L("地址发现失败，已使用中继") : directReason.empty() ? L("检测中") : directReason;
        const auto candidateCount = JsonInt(direct, L"candidateCount");
        const auto attemptCount = JsonInt(direct, L"attemptCount");
        refreshed->details = candidateCount ? std::wstring(L("IPv6 候选 ")) + std::to_wstring(candidateCount) + L("，已尝试 ") + std::to_wstring(attemptCount) : L"";
        const auto endpoint = JsonString(direct, L"endpoint");
        if (!endpoint.empty()) refreshed->details += (refreshed->details.empty() ? L"" : L"\n") + std::wstring(L"UDP ") + endpoint;
        const auto lastError = status.GetNamedObject(L"lastError", JsonObject{});
        refreshed->statusError = localWaiting ? L("正在等待局域网访问") + (JsonString(status, L"localNetworkAccess").empty() ? L"" : L" (" + JsonString(status, L"localNetworkAccess") + L")") : FailureMessage(lastError);
        const auto dns = status.GetNamedObject(L"dns", JsonObject{});
        const auto dnsState = JsonString(dns, L"state", L"inactive");
        const std::wstring dnsLabel = dnsState == L"ready" ? L("就绪") : dnsState == L"preparing" ? L("正在读取") : dnsState == L"degraded" ? L("降级") : L("未连接");
        refreshed->dnsSuffix = JsonString(dns, L"suffix");
        refreshed->dnsText = dnsLabel + L" · " + refreshed->dnsSuffix;
        const auto dnsError = FailureMessage(dns.GetNamedObject(L"lastError", JsonObject{}));
        if (!dnsError.empty()) refreshed->details += L"\n" + dnsError;
        std::wstring errorList;
        if (result.HasKey(L"errors") && result.GetNamedValue(L"errors").ValueType() == JsonValueType::Array) {
            for (const auto& value : result.GetNamedArray(L"errors")) {
                if (value.ValueType() != JsonValueType::Object) continue;
                const auto description = FailureMessage(value.GetObject());
                if (!description.empty()) errorList += (errorList.empty() ? L"" : L"\n") + description;
            }
        }
        if (localWaiting) refreshed->details += (refreshed->details.empty() ? L"" : L"\n") + refreshed->statusError;
        if (JsonBool(result, L"lanOverlap")) { if (!errorList.empty()) errorList += L"\n"; errorList += L("本地路由冲突：仅保留 NAS 隧道路由"); }
        if (!errorList.empty()) refreshed->details += (refreshed->details.empty() ? L"" : L"\n") + errorList;
        if (refreshed->details.empty()) refreshed->details = L("无异常");
        refreshed->diagnosticJson = result.Stringify().c_str();
        PostPayload(kStatusReady, refreshed.release(), 0);
    } catch (const winrt::hresult_error& error) {
        auto* refreshed = new RefreshResult{std::wstring(L("客户端服务暂不可用：")) + std::wstring(error.message().c_str()), L"", L"DAEMON_UNAVAILABLE", false};
        PostPayload(kStatusReady, refreshed, 0);
    } catch (...) {
        auto* refreshed = new RefreshResult{L("无法读取连接状态"), L"", L"DAEMON_UNAVAILABLE", false};
        PostPayload(kStatusReady, refreshed, 0);
    }
}

void PublishDaemonError(const wchar_t* message) {
    PostPayload(kStatusReady, new RefreshResult{message, L"", L"DAEMON_UNAVAILABLE", false}, 0);
}

void EnsureDaemonFromWorker() {
    winrt::init_apartment(winrt::apartment_type::multi_threaded);
    struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
    try {
        (void)CallIpc(L"status");
        PostMessageW(g_window, kDaemonReady, 0, 0);
        return;
    } catch (...) {}

    std::vector<wchar_t> executablePath(32768);
    const auto pathLength = GetModuleFileNameW(nullptr, executablePath.data(), static_cast<DWORD>(executablePath.size()));
    if (pathLength == 0 || pathLength >= std::size(executablePath)) {
        PublishDaemonError(L("无法定位 FnCPN 后台程序"));
        return;
    }
    std::wstring directory(executablePath.data(), pathLength);
    const auto separator = directory.find_last_of(L"\\/");
    if (separator == std::wstring::npos) return;
    directory.resize(separator);
    const auto parent = directory.find_last_of(L"\\/");
    if (parent == std::wstring::npos) return;
    directory.resize(parent);
    const auto clientPath = directory + L"\\fncpn.exe";
    if (GetFileAttributesW(clientPath.c_str()) == INVALID_FILE_ATTRIBUTES) {
        PublishDaemonError(L("找不到 Go 客户端，请修复 FnCPN 安装"));
        return;
    }

    std::wstring command = L"\"" + clientPath + L"\" status --json";
    STARTUPINFOW startup{sizeof(startup)};
    startup.dwFlags = STARTF_USESHOWWINDOW;
    startup.wShowWindow = SW_HIDE;
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(clientPath.c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
                        nullptr, directory.c_str(), &startup, &process)) {
        PublishDaemonError(L("无法启动 FnCPN 后台服务"));
        return;
    }
    CloseHandle(process.hThread);
    const auto deadline = GetTickCount64() + 60000;
    DWORD wait = WAIT_TIMEOUT;
    while (!g_closing && GetTickCount64() < deadline) {
        wait = WaitForSingleObject(process.hProcess, 250);
        if (wait != WAIT_TIMEOUT) break;
    }
    DWORD exitCode = 1;
    if (wait == WAIT_TIMEOUT) TerminateProcess(process.hProcess, ERROR_TIMEOUT);
    else if (wait == WAIT_OBJECT_0) GetExitCodeProcess(process.hProcess, &exitCode);
    CloseHandle(process.hProcess);
    if (g_closing) return;
    if (wait != WAIT_OBJECT_0 || exitCode != 0) {
        PublishDaemonError(wait == WAIT_TIMEOUT ? L("FnCPN 后台服务启动超时") : L("FnCPN 后台服务启动失败"));
        return;
    }
    PostMessageW(g_window, kDaemonReady, 0, 0);
}

void WatchStatusFromWorker() {
    winrt::init_apartment(winrt::apartment_type::multi_threaded);
    struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
    double generation = 0;
    while (!g_closing) {
        try {
            JsonObject parameters; parameters.Insert(L"after", JsonValue::CreateNumberValue(generation));
            auto result = CallIpc(L"watch-client-status", &parameters).GetNamedObject(L"result");
            generation = result.GetNamedNumber(L"generation");
            if (JsonBool(result, L"changed")) PostMessageW(g_window, kStatusChanged, 0, 0);
        } catch (...) { Sleep(1000); }
    }
}

void StartRefresh() {
    if (!g_busy && !g_loginInFlight && !g_operationInFlight) {
        g_busy = true;
        StartWorker(RefreshFromWorker);
    }
}

std::wstring NormalizeFnId(std::wstring value) {
    while (!value.empty() && iswspace(value.front())) value.erase(value.begin());
    while (!value.empty() && iswspace(value.back())) value.pop_back();
    std::transform(value.begin(), value.end(), value.begin(), towlower);
    constexpr std::wstring_view suffix = L".fnos.net";
    if (value.size() > suffix.size() && value.ends_with(suffix)) value.resize(value.size() - suffix.size());
    if (value.empty() || value.size() > 63 || value.front() == L'-' || value.back() == L'-') return {};
    for (const auto ch : value) if (!((ch >= L'a' && ch <= L'z') || (ch >= L'0' && ch <= L'9') || ch == L'-')) return {};
    return value;
}

void PublishLoginResult(std::wstring message, unsigned generation) {
    PostPayload(kLoginResult, new std::wstring(std::move(message)), generation);
}

void LoginFromWorker(std::wstring fnId, std::wstring username, std::wstring password, std::wstring requestId, bool externalOwner, unsigned generation) {
    winrt::init_apartment(winrt::apartment_type::multi_threaded);
    struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
    try {
        if (requestId.empty()) {
            JsonObject beginParameters;
            beginParameters.Insert(L"fnId", JsonValue::CreateStringValue(fnId));
            auto begin = CallIpc(L"authorization-begin", &beginParameters).GetNamedObject(L"result");
            requestId = begin.GetNamedString(L"requestId");
            PostPayload(kAuthorizationStarted, new std::wstring(requestId), generation);
        }
        JsonObject authorizeParameters;
        authorizeParameters.Insert(L"requestId", JsonValue::CreateStringValue(requestId));
        authorizeParameters.Insert(L"fnId", JsonValue::CreateStringValue(fnId));
        authorizeParameters.Insert(L"username", JsonValue::CreateStringValue(username));
        authorizeParameters.Insert(L"password", JsonValue::CreateStringValue(password));
        (void)CallIpc(L"authorize-native", &authorizeParameters);
        if (!externalOwner) {
            JsonObject waitParameters;
            waitParameters.Insert(L"requestId", JsonValue::CreateStringValue(requestId));
            auto waited = CallIpc(L"authorization-wait", &waitParameters).GetNamedObject(L"result");
            if (JsonString(waited, L"state") != L"succeeded") {
                PublishLoginResult(L("授权未完成，请检查账号或登录状态后重试"), generation);
                return;
            }
        }
        PublishLoginResult(L"", generation);
    } catch (const winrt::hresult_error& error) {
        PublishLoginResult(error.message().c_str(), generation);
    } catch (...) {
        PublishLoginResult(L("登录失败，请重试"), generation);
    }
}

void CancelAuthorization(const std::wstring& requestId) {
    if (requestId.empty()) return;
    StartWorker([requestId] {
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
        JsonObject parameters; parameters.Insert(L"requestId", JsonValue::CreateStringValue(requestId));
        try { (void)CallIpc(L"authorization-cancel", &parameters); } catch (...) {}
    });
}

void PlaceControl(HWND control, int x, int y, int width, int height) {
    SetWindowPos(control, nullptr, x, y, width, height, SWP_NOZORDER | SWP_NOACTIVATE);
}

HWND AddControl(const wchar_t* klass, const wchar_t* text, DWORD style, int id) {
    if(wcscmp(klass,L"EDIT")==0 && !(style&ES_MULTILINE)) style &= ~WS_BORDER;
    const DWORD tabStop = (wcscmp(klass, L"BUTTON") == 0 || wcscmp(klass, L"EDIT") == 0) ? WS_TABSTOP : 0;
    auto control = CreateWindowExW(0, klass, text, WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS | style | tabStop, 0, 0, 0, 0,
                          g_window, reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)), GetModuleHandleW(nullptr), nullptr);
    if (wcscmp(klass, L"BUTTON") == 0) SetWindowSubclass(control,
        [](HWND child, UINT message, WPARAM wParam, LPARAM lParam, UINT_PTR, DWORD_PTR) -> LRESULT {
            if (message == WM_PAINT && GetDlgCtrlID(child) == kAutostart) {
                PAINTSTRUCT paint{}; auto dc = BeginPaint(child,&paint); RECT area{}; GetClientRect(child,&area);
                FillRect(dc,&area,g_backgroundBrush ? g_backgroundBrush : GetSysColorBrush(COLOR_BTNFACE));
                const int size=MulDiv(16,GetDpiForWindow(child),96);
                RECT box{0,(area.bottom-size)/2,size,(area.bottom+size)/2};
                const bool checked=SendMessageW(child,BM_GETCHECK,0,0)==BST_CHECKED;
                FillRect(dc,&box,GetSysColorBrush(COLOR_WINDOW)); FrameRect(dc,&box,GetSysColorBrush(COLOR_GRAYTEXT));
                if(checked) { auto pen=CreatePen(PS_SOLID,2,GetSysColor(COLOR_BTNTEXT)); auto old=SelectObject(dc,pen);
                    MoveToEx(dc,box.left+size/4,box.top+size/2,nullptr); LineTo(dc,box.left+size/2,box.bottom-size/4); LineTo(dc,box.right-size/5,box.top+size/4);
                    SelectObject(dc,old); DeleteObject(pen); }
                auto previous=SelectObject(dc,g_font); area.left=size+MulDiv(6,GetDpiForWindow(child),96);
                SetBkMode(dc,TRANSPARENT); SetTextColor(dc,GetSysColor(COLOR_BTNTEXT)); auto text=GetText(child);
                DrawTextW(dc,text.c_str(),static_cast<int>(text.size()),&area,DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX);
                SelectObject(dc,previous); EndPaint(child,&paint); return 0;
            }
            if (message == WM_SETCURSOR && LOWORD(lParam) == HTCLIENT && IsWindowEnabled(child)) {
                SetCursor(LoadCursorW(nullptr, IDC_HAND)); return TRUE;
            }
            if (message == WM_MOUSEMOVE && !GetPropW(child,L"Hovered")) { SetPropW(child,L"Hovered",reinterpret_cast<HANDLE>(1)); TRACKMOUSEEVENT track{sizeof(track),TME_LEAVE,child,0}; TrackMouseEvent(&track); InvalidateRect(child,nullptr,FALSE); }
            if (message == WM_MOUSELEAVE) { RemovePropW(child,L"Hovered"); InvalidateRect(child,nullptr,FALSE); }
            return DefSubclassProc(child, message, wParam, lParam);
        }, 1, 0);
    if (wcscmp(klass,L"EDIT") == 0 && !(style & ES_MULTILINE)) {
        auto frame=CreateWindowExW(WS_EX_CONTROLPARENT,L"STATIC",L"",WS_CHILD|WS_VISIBLE|WS_BORDER|WS_CLIPCHILDREN|SS_LEFT,
            0,0,0,0,g_window,nullptr,GetModuleHandleW(nullptr),nullptr);
        SetPropW(control,L"InputFrame",frame); SetParent(control,frame);
        SetWindowLongW(control,GWL_STYLE,GetWindowLongW(control,GWL_STYLE)&~WS_BORDER);
        SetWindowSubclass(control,[](HWND child,UINT message,WPARAM wParam,LPARAM lParam,UINT_PTR,DWORD_PTR)->LRESULT {
            // The outer frame owns the only border. Keep the inner edit's entire
            // rectangle as client space, including after DPI/theme changes.
            if(message==WM_NCPAINT || message==WM_NCCALCSIZE) return 0;
            return DefSubclassProc(child,message,wParam,lParam);
        },2,0);
        SetWindowTheme(control,L"",L"");
        SetWindowPos(control,nullptr,0,0,0,0,SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_FRAMECHANGED);
        SetWindowSubclass(frame,[](HWND child,UINT message,WPARAM wParam,LPARAM lParam,UINT_PTR,DWORD_PTR)->LRESULT {
            if(message==WM_LBUTTONDOWN) { SetFocus(GetWindow(child,GW_CHILD)); return 0; }
            return DefSubclassProc(child,message,wParam,lParam);
        },3,0);
    }
    return control;
}

int Scale(HWND window, int value) { return MulDiv(value, GetDpiForWindow(window), 96); }
double g_layoutScaleY = 1.0;

void Move(HWND window, HWND control, int x, int y, int width, int height) {
    if(auto frame=static_cast<HWND>(GetPropW(control,L"InputFrame"))) {
        const int boxHeight=Scale(window,static_cast<int>(height*g_layoutScaleY));
        SetWindowPos(frame,nullptr,Scale(window,x),Scale(window,static_cast<int>(y*g_layoutScaleY)-g_scrollY),Scale(window,width),boxHeight,SWP_NOZORDER|SWP_NOACTIVATE);
        auto dc=GetDC(control); auto old=SelectObject(dc,g_font); TEXTMETRICW metrics{}; GetTextMetricsW(dc,&metrics); SelectObject(dc,old); ReleaseDC(control,dc);
        SetWindowPos(control,nullptr,Scale(window,4),std::max(0,(boxHeight-static_cast<int>(metrics.tmHeight)-2)/2),Scale(window,width-8),metrics.tmHeight+1,SWP_NOZORDER|SWP_NOACTIVATE);
        return;
    }
    if (control) SetWindowPos(control, nullptr, Scale(window, x), Scale(window, static_cast<int>(y * g_layoutScaleY) - g_scrollY), Scale(window, width), Scale(window, static_cast<int>(height * g_layoutScaleY)), SWP_NOZORDER | SWP_NOACTIVATE);
}

void ShowControls(bool show, std::initializer_list<HWND> controls) {
    for (HWND control : controls) if (control) {
        if(auto frame=static_cast<HWND>(GetPropW(control,L"InputFrame"))) ShowWindow(frame,show ? SW_SHOW : SW_HIDE);
        ShowWindow(control, show ? SW_SHOW : SW_HIDE);
    }
}

std::vector<HIMAGELIST> g_buttonImages;
HICON g_nasButtonIcon{}, g_dnsButtonIcon{};
std::wstring g_diagnosticBase, g_adminError;

void UpdateButtonIcons(HWND window) {
    if (g_nasButtonIcon) DestroyIcon(g_nasButtonIcon);
    if (g_dnsButtonIcon) DestroyIcon(g_dnsButtonIcon);
    for (auto images : g_buttonImages) ImageList_Destroy(images);
    g_buttonImages.clear();
    const int size = Scale(window, 16);
    auto dc = GetDC(window);
    auto memory = CreateCompatibleDC(dc);
    auto font = CreateFontW(-size, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE, DEFAULT_CHARSET,
        OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, ANTIALIASED_QUALITY, DEFAULT_PITCH, L"Segoe MDL2 Assets");
    auto oldFont = SelectObject(memory, font);
    // Windows' native symbol font keeps icons sharp at every display scale.
    for (auto [id, glyph] : std::initializer_list<std::pair<int, wchar_t>>{
        {kConnect, L'\uE703'}, {kRelogin, 0xE77B}, {kRetry, 0xE72C}, {kChangeServer, 0xE8AB},
        {kCopyDNSSuffix, 0xE8C8}, {kAdmin, 0xE774}, {kCopyDiagnostics, 0xE8C8}, {kOpenLog, 0xE8B7},
        {kRefresh, 0xE72C}, {kExit, 0xE7E8}, {kOpenNas, 0xE8A7}}) {
        auto bitmap = CreateCompatibleBitmap(dc, size, size);
        auto oldBitmap = SelectObject(memory, bitmap);
        RECT rect{0, 0, size, size}; FillRect(memory, &rect, static_cast<HBRUSH>(GetStockObject(WHITE_BRUSH)));
        SetBkColor(memory, RGB(255,255,255)); SetTextColor(memory, GetSysColor(COLOR_BTNTEXT));
        DrawTextW(memory, &glyph, 1, &rect, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        SelectObject(memory, oldBitmap);
        auto images = ImageList_Create(size, size, ILC_COLOR32 | ILC_MASK, 1, 0);
        ImageList_AddMasked(images, bitmap, RGB(255,255,255)); DeleteObject(bitmap);
        BUTTON_IMAGELIST image{images, {Scale(window, 6), 0, Scale(window, 4), 0}, static_cast<UINT>((id == kOpenNas || id == kCopyDNSSuffix) ? BUTTON_IMAGELIST_ALIGN_CENTER : BUTTON_IMAGELIST_ALIGN_LEFT)};
        SendMessageW(GetDlgItem(window, id), BCM_SETIMAGELIST, 0, reinterpret_cast<LPARAM>(&image));
        if (id == kOpenNas) {
            g_nasButtonIcon = ImageList_GetIcon(images, 0, ILD_NORMAL);
            SendMessageW(g_openNas, BM_SETIMAGE, IMAGE_ICON, reinterpret_cast<LPARAM>(g_nasButtonIcon));
        }
        if (id == kCopyDNSSuffix) {
            g_dnsButtonIcon = ImageList_GetIcon(images, 0, ILD_NORMAL);
        }
        g_buttonImages.push_back(images);
        SetWindowLongW(GetDlgItem(window,id),GWL_STYLE,(GetWindowLongW(GetDlgItem(window,id),GWL_STYLE) & ~BS_TYPEMASK) | BS_OWNERDRAW);
    }
    SelectObject(memory, oldFont); DeleteObject(font); DeleteDC(memory); ReleaseDC(window, dc);
}

void UpdateDiagnosticScrollBar() {
    RECT rect{}; SendMessageW(g_diagnosticValue, EM_GETRECT, 0, reinterpret_cast<LPARAM>(&rect));
    auto dc = GetDC(g_diagnosticValue); auto previous = SelectObject(dc, g_monoFont);
    TEXTMETRICW metrics{}; GetTextMetricsW(dc, &metrics); SelectObject(dc, previous); ReleaseDC(g_diagnosticValue, dc);
    const int lines = static_cast<int>(SendMessageW(g_diagnosticValue, EM_GETLINECOUNT, 0, 0));
    ShowScrollBar(g_diagnosticValue, SB_VERT, lines * metrics.tmHeight > rect.bottom - rect.top);
}

void UpdateFont(HWND window) {
    HIGHCONTRASTW contrast{sizeof(contrast)};
    SystemParametersInfoW(SPI_GETHIGHCONTRAST, sizeof(contrast), &contrast, 0);
    g_highContrast = (contrast.dwFlags & HCF_HIGHCONTRASTON) != 0;
    g_backgroundColor = g_highContrast ? GetSysColor(COLOR_BTNFACE) : RGB(245, 246, 248);
    if (g_backgroundBrush) DeleteObject(g_backgroundBrush);
    g_backgroundBrush = CreateSolidBrush(g_backgroundColor);
    std::array<HFONT, 5> previous{g_font, g_titleFont, g_deviceFont, g_statusFont, g_monoFont};
    auto makeFont = [window](int points, int weight, const wchar_t* family) {
        return CreateFontW(-MulDiv(points, GetDpiForWindow(window), 72), 0, 0, 0, weight, FALSE, FALSE, FALSE,
            DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH, family);
    };
    g_font = makeFont(10, FW_NORMAL, L"Segoe UI");
    g_titleFont = makeFont(21, FW_SEMIBOLD, L"Segoe UI");
    g_deviceFont = makeFont(14, FW_SEMIBOLD, L"Segoe UI");
    g_statusFont = makeFont(16, FW_SEMIBOLD, L"Segoe UI");
    g_monoFont = makeFont(9, FW_NORMAL, L"Consolas");
    EnumChildWindows(window, [](HWND child, LPARAM font) -> BOOL { SendMessageW(child, WM_SETFONT, font, TRUE); return TRUE; }, reinterpret_cast<LPARAM>(g_font));
    for (HWND input : {g_fnIdInput,g_usernameInput,g_passwordInput}) {
        SendMessageW(input,EM_SETMARGINS,EC_LEFTMARGIN|EC_RIGHTMARGIN,MAKELPARAM(Scale(window,6),Scale(window,6)));
        SetWindowPos(input,nullptr,0,0,0,0,SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE|SWP_FRAMECHANGED);
    }
    SendMessageW(g_brand, WM_SETFONT, reinterpret_cast<WPARAM>(g_titleFont), TRUE);
    SendMessageW(g_identity, WM_SETFONT, reinterpret_cast<WPARAM>(g_deviceFont), TRUE);
    SendMessageW(g_state, WM_SETFONT, reinterpret_cast<WPARAM>(g_statusFont), TRUE);
    SendMessageW(g_diagnosticValue, WM_SETFONT, reinterpret_cast<WPARAM>(g_monoFont), TRUE);
    UpdateButtonIcons(window);
    for (auto font : previous) if (font) DeleteObject(font);
    if (g_headerImage) DestroyIcon(g_headerImage);
    LoadIconWithScaleDown(GetModuleHandleW(nullptr),MAKEINTRESOURCEW(1),Scale(window,48),Scale(window,48),&g_headerImage);
    SendMessageW(g_headerIcon, STM_SETICON, reinterpret_cast<WPARAM>(g_headerImage), 0);
    InvalidateRect(window, nullptr, TRUE);
}

void Layout(HWND window) {
    std::wstring diagnostic=g_diagnosticBase;
    if ((!GetText(g_statusError).empty() || !g_adminError.empty()) && diagnostic==L("无异常")) diagnostic.clear();
    for(const auto& error : {GetText(g_statusError),g_adminError}) if(!error.empty()) {
        if(!diagnostic.empty()) diagnostic += L"\r\n\r\n";
        diagnostic += error;
    }
    if(g_diagnosticValue && GetText(g_diagnosticValue)!=diagnostic) SetWindowTextW(g_diagnosticValue,diagnostic.c_str());
    RECT client{}; GetClientRect(window, &client);
    const int width = MulDiv(client.right, 96, GetDpiForWindow(window));
    const int height = MulDiv(client.bottom, 96, GetDpiForWindow(window));
    const int margin = 28;
    const int contentWidth = std::max(0, width - margin * 2);
    const bool errorVisible = false;
    const bool repairVisible = g_configured && !g_privilegedAvailable;
    const int naturalHeight = g_viewLogin ? 510 : 756 + (errorVisible ? 44 : 0) + (repairVisible ? 42 : 0);
    g_layoutScaleY = std::clamp(static_cast<double>(height) / naturalHeight, 0.82, 1.0);
    const int contentHeight = static_cast<int>(naturalHeight * g_layoutScaleY);
    SCROLLINFO scroll{sizeof(scroll), SIF_RANGE | SIF_PAGE | SIF_POS};
    scroll.nMax = contentHeight - 1; scroll.nPage = std::max(1, height);
    g_scrollY = std::clamp(g_scrollY, 0, std::max(0, contentHeight - height));
    scroll.nPos = g_scrollY; SetScrollInfo(window, SB_VERT, &scroll, TRUE);
    Move(window, g_headerIcon, margin, 28, 48, 48);
    auto titleDc = GetDC(window); auto oldTitleFont = SelectObject(titleDc, g_titleFont);
    SIZE titleSize{}; GetTextExtentPoint32W(titleDc, L"FnCPN", 5, &titleSize);
    SelectObject(titleDc, oldTitleFont); ReleaseDC(window, titleDc);
    const int titleWidth = MulDiv(titleSize.cx, 96, GetDpiForWindow(window));
    Move(window, g_brand, margin + 64, 23, titleWidth + 2, 48);
    Move(window, g_versionLabel, margin + 64 + titleWidth + 10, 37, 100, 26);
    Move(window, g_identity, margin, 101, contentWidth, 34);
    if (g_viewLogin) {
        Move(window, g_fnIdLabel, margin, 158, contentWidth, 24);
        Move(window, g_fnIdInput, margin, 187, contentWidth, 24);
        Move(window, g_usernameLabel, margin, 240, contentWidth, 24);
        Move(window, g_usernameInput, margin, 269, contentWidth, 24);
        Move(window, g_passwordLabel, margin, 322, contentWidth, 24);
        Move(window, g_passwordInput, margin, 351, contentWidth, 24);
        Move(window, g_loginError, margin, 398, contentWidth, 48);
        Move(window, g_loginButton, margin, 458, 158, 34);
        Move(window, g_loginCancel, margin + 168, 458, 110, 34);
        Move(window, g_loginReturn, margin + 168, 458, 158, 34);
        ShowControls(true, {g_fnIdLabel, g_fnIdInput, g_usernameLabel, g_usernameInput, g_passwordLabel, g_passwordInput, g_loginError, g_loginButton});
        ShowControls(g_externalAuthorization || g_loginInFlight, {g_loginCancel});
        ShowControls(g_configured && !g_loginInFlight && !g_externalAuthorization, {g_loginReturn});
        EnableWindow(g_fnIdInput, !g_loginInFlight && !g_externalAuthorization);
        EnableWindow(g_usernameInput, !g_loginInFlight); EnableWindow(g_passwordInput, !g_loginInFlight);
        EnableWindow(g_loginButton, !g_loginInFlight);
        ShowControls(false, {GetDlgItem(window, kRefresh), GetDlgItem(window, kExit), g_state, g_connect, g_retry, g_relogin, g_changeServer, g_admin, g_copyDiagnostics, g_openLog, g_openNas, g_repair, g_autostart, g_statusError, g_diagnosticValue, g_diagnosticsLabel, g_dnsLabel, g_dnsValue, g_copyDNSSuffix});
        for (int i = 0; i < 7; ++i) ShowControls(false, {g_values[i], g_fieldLabels[i]});
    } else {
        Move(window, g_state, margin, 142, contentWidth, 36);
        const int valueX = margin + 126;
        for (int i = 0; i < 7; ++i) {
            const int row = i < 4 ? i : i + 1; // DNS follows the handshake, matching macOS.
            Move(window, g_fieldLabels[i], margin, 197 + row * 32, 118, 26);
            Move(window, g_values[i], valueX, 197 + row * 32, contentWidth - 126, 26);
        }
        Move(window, g_values[6], valueX, 197 + 7 * 32, 158, 26);
        auto valueDc=GetDC(g_values[6]); auto previousValueFont=SelectObject(valueDc,g_font); SIZE ipSize{}; auto ip=GetText(g_values[6]);
        GetTextExtentPoint32W(valueDc,ip.c_str(),static_cast<int>(ip.size()),&ipSize); SelectObject(valueDc,previousValueFont); ReleaseDC(g_values[6],valueDc);
        Move(window, g_openNas, valueX + MulDiv(ipSize.cx,96,GetDpiForWindow(window)) + 6, 197 + 7 * 32, 22, 26);
        const int dnsY = 197 + 4 * 32;
        Move(window, g_dnsLabel, margin, dnsY, 118, 26);
        auto dnsDc = GetDC(g_dnsValue); auto dnsFont = SelectObject(dnsDc, g_font); SIZE dnsSize{};
        auto dnsText = GetText(g_dnsValue);
        GetTextExtentPoint32W(dnsDc, dnsText.c_str(), static_cast<int>(dnsText.size()), &dnsSize);
        SelectObject(dnsDc, dnsFont); ReleaseDC(g_dnsValue, dnsDc);
        // Static controls must not cover the icon's hit-test/paint area. Keep the
        // copy action next to short text and reserve room when text is truncated.
        const int dnsWidth = std::clamp(MulDiv(dnsSize.cx, 96, GetDpiForWindow(window)) + 2, 1, std::max(1, contentWidth - 126 - 32));
        Move(window, g_dnsValue, valueX, dnsY, dnsWidth, 26);
        Move(window, g_copyDNSSuffix, valueX + dnsWidth + 6, dnsY, 26, 26);
        EnableWindow(g_copyDNSSuffix, !g_dnsSuffix.empty());
        Move(window, g_connect, margin, 469, 150, 34);
        Move(window, g_relogin, margin + 160, 469, 130, 34);
        Move(window, g_retry, margin + 300, 469, 140, 34);
        int y = 519;
        if (errorVisible) { Move(window, g_statusError, margin, y, contentWidth, 38); y += 44; }
        Move(window, g_diagnosticsLabel, margin, y, contentWidth, 25); y += 32;
        Move(window, g_diagnosticValue, margin, y, contentWidth, 82); y += 102;
        UpdateDiagnosticScrollBar();
        // One aligned toolbar; compact secondary actions retain accessible text.
        const std::array<HWND, 6> tools{g_changeServer, g_admin, g_copyDiagnostics, g_openLog, GetDlgItem(window, kRefresh), GetDlgItem(window, kExit)};
        const std::array<int, 6> widths{112, 112, 68, 68, 68, 76};
        int x = margin;
        for (size_t i = 0; i < tools.size(); ++i) {
            if (i == 1 && !(g_configured && g_administrator)) continue;
            Move(window, tools[i], i == 5 ? width - margin - widths[i] : x, y, widths[i], 32); x += widths[i] + 8;
        }
        y += 45;
        Move(window, g_autostart, margin, y, 126, 28); y += 35;
        if (repairVisible) Move(window, g_repair, margin, y, contentWidth, 32);
        ShowControls(true, {GetDlgItem(window, kExit), g_state, g_connect, g_retry, g_relogin, g_changeServer, g_copyDiagnostics, g_openLog, g_openNas, g_autostart, g_diagnosticValue, g_diagnosticsLabel, GetDlgItem(window, kRefresh)});
        ShowControls(true, {g_dnsLabel, g_dnsValue, g_copyDNSSuffix});
        ShowControls(false, {g_statusError});
        ShowControls(g_configured && g_administrator, {g_admin}); EnableWindow(g_admin, !g_adminOpening);
        ShowControls(repairVisible, {g_repair});
        ShowControls(false, {g_fnIdLabel, g_fnIdInput, g_usernameLabel, g_usernameInput, g_passwordLabel, g_passwordInput, g_loginError, g_loginButton, g_loginReturn, g_loginCancel});
        for (HWND control : g_fieldLabels) ShowWindow(control, SW_SHOW);
        for (HWND control : g_values) ShowWindow(control, SW_SHOW);
    }
    InvalidateRect(window,nullptr,TRUE);
}

void ShowMainWindow(HWND window) {
    ShowWindow(window, SW_SHOW);
    ShowWindow(window, SW_RESTORE);
    SetForegroundWindow(window);
}

bool IsConnectedState(const std::wstring& state) {
    return state == L"LOCAL" || state == L"DIRECT" || state == L"RELAY" || state == L"PROBING" || state == L"RECONNECTING";
}

void SetAutoStart(bool enabled) {
    HKEY key{};
    if (RegCreateKeyExW(HKEY_CURRENT_USER, L"Software\\Microsoft\\Windows\\CurrentVersion\\Run", 0, nullptr, 0,
        KEY_SET_VALUE, nullptr, &key, nullptr) != ERROR_SUCCESS) return;
    if (enabled) {
        std::vector<wchar_t> path(32768);
        const auto length = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
        if (!length || length >= path.size()) { RegCloseKey(key); return; }
        std::wstring value = L"\"" + std::wstring(path.data(), length) + L"\" --background";
        RegSetValueExW(key, L"FnCPN", 0, REG_SZ, reinterpret_cast<const BYTE*>(value.c_str()), static_cast<DWORD>((value.size() + 1) * sizeof(wchar_t)));
    } else RegDeleteValueW(key, L"FnCPN");
    RegCloseKey(key);
    g_autoStart = enabled;
    if (g_autostart) SendMessageW(g_autostart, BM_SETCHECK, enabled ? BST_CHECKED : BST_UNCHECKED, 0);
}

void RunIpcOperation(const wchar_t* method, const wchar_t* busyText) {
    if (g_operationInFlight || g_loginInFlight) return;
    g_operationInFlight = true;
    g_operationError.clear();
    g_busy = true;
    SetStatus(busyText);
    std::wstring operation(method);
    StartWorker([operation] {
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
        auto errorText = std::make_unique<std::wstring>();
        try { (void)CallIpc(operation.c_str()); }
        catch (const winrt::hresult_error& error) { *errorText = error.message().c_str(); }
        PostPayload(kOperationReady, errorText.release(), 0);
    });
}

DWORD RunElevated(const wchar_t* program, const std::wstring& arguments) {
    wchar_t directory[MAX_PATH]{}; GetSystemDirectoryW(directory, MAX_PATH);
    const auto path = std::wstring(directory) + L"\\" + program;
    SHELLEXECUTEINFOW execute{sizeof(execute)};
    execute.fMask = SEE_MASK_NOCLOSEPROCESS; execute.lpVerb = L"runas";
    execute.lpFile = path.c_str(); execute.lpParameters = arguments.c_str(); execute.nShow = SW_HIDE;
    if (!ShellExecuteExW(&execute)) throw winrt::hresult_error(HRESULT_FROM_WIN32(GetLastError()));
    if (!execute.hProcess) throw winrt::hresult_error(E_FAIL);
    while(WaitForSingleObject(execute.hProcess,250)==WAIT_TIMEOUT) { if(g_closing) { CloseHandle(execute.hProcess); throw winrt::hresult_error(HRESULT_FROM_WIN32(ERROR_CANCELLED)); } }
    DWORD code = 1; GetExitCodeProcess(execute.hProcess, &code); CloseHandle(execute.hProcess);
    return code;
}

void RepairServiceFromWorker() {
    winrt::init_apartment(winrt::apartment_type::multi_threaded);
    struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
    std::wstring error;
    try {
        DWORD result = RunElevated(L"sc.exe", L"start FnCPNClient");
        if (result == 1060 || result == 2 || result == 193 || result == 1058) {
            wchar_t product[64]{}; DWORD size = sizeof(product);
            if (RegGetValueW(HKEY_LOCAL_MACHINE, L"Software\\FnCPN", L"ProductCode", RRF_RT_REG_SZ, nullptr, product, &size) != ERROR_SUCCESS)
                throw winrt::hresult_error(E_FAIL, L("找不到 MSI 产品记录，请从原安装包运行修复。"));
            GUID guid{};
            if (FAILED(CLSIDFromString(product, &guid))) throw winrt::hresult_error(E_INVALIDARG);
            result = RunElevated(L"msiexec.exe", std::wstring(L"/fa ") + product + L" /qb");
        }
        if (result == 1602) throw winrt::hresult_error(HRESULT_FROM_WIN32(ERROR_CANCELLED));
        if (result != 0 && result != 1056 && result != 3010) throw winrt::hresult_error(HRESULT_FROM_WIN32(result));
        bool ready = false;
        const auto deadline = GetTickCount64() + 15000;
        while (GetTickCount64() < deadline && !g_closing) {
            try { ready = JsonBool(CallIpc(L"diagnose").GetNamedObject(L"result"), L"privilegedAvailable"); } catch (...) {}
            if (ready) break;
            Sleep(250);
        }
        if (!ready) error = L("FnCPN 网络服务未能就绪。请查看诊断信息。");
    } catch (const winrt::hresult_error& failure) {
        error = failure.code() == HRESULT_FROM_WIN32(ERROR_CANCELLED) ? L("已取消管理员授权。") : failure.message().c_str();
    }
    PostPayload(kOperationReady, new std::wstring(std::move(error)), 0);
}

void CopyText(HWND window, const std::wstring& serialized) {
    if (!OpenClipboard(window)) return;
    EmptyClipboard();
    const auto text = serialized.c_str();
    const size_t bytes = (wcslen(text) + 1) * sizeof(wchar_t);
    HGLOBAL memory = GlobalAlloc(GMEM_MOVEABLE, bytes);
    if (memory) {
        void* target = GlobalLock(memory);
        if (target) { memcpy(target, text, bytes); GlobalUnlock(memory); SetClipboardData(CF_UNICODETEXT, memory); }
        else GlobalFree(memory);
    }
    CloseClipboard();
}

void CopyDiagnostics(HWND window) {
    CopyText(window, g_diagnostics ? std::wstring(g_diagnostics.Stringify()) : L"{}");
}

void OpenLogs() {
    wchar_t local[MAX_PATH]{};
    if (SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA, nullptr, SHGFP_TYPE_CURRENT, local) == S_OK)
        ShellExecuteW(nullptr, L"open", (std::wstring(local) + L"\\FnCPN\\Logs").c_str(), nullptr, nullptr, SW_SHOWNORMAL);
}

void OpenNas() {
    if (g_diagnostics && g_diagnostics.HasKey(L"nasAddress")) {
        const auto address = JsonString(g_diagnostics, L"nasAddress");
        if (!address.empty()) ShellExecuteW(nullptr, L"open", (L"http://" + address).c_str(), nullptr, nullptr, SW_SHOWNORMAL);
    }
}

bool ParseAuthorizationUri(const std::wstring& argument, std::wstring& fnId, std::wstring& request) {
    fnId.clear(); request.clear();
    constexpr std::wstring_view prefix = L"fncpn://authorize/";
    if (argument.size() < prefix.size() || _wcsnicmp(argument.c_str(), prefix.data(), prefix.size()) != 0) return false;
    const auto query = argument.find(L'?');
    if (query == std::wstring::npos || argument.find(L'#') != std::wstring::npos) return false;
    fnId = NormalizeFnId(argument.substr(prefix.size(), query - prefix.size()));
    if (fnId.empty()) return false;
    size_t position = query + 1;
    while (position < argument.size()) {
        const auto end = argument.find(L'&', position);
        const auto field = argument.substr(position, end == std::wstring::npos ? end : end - position);
        if (field.starts_with(L"request=")) {
            if (!request.empty()) return false;
            request = field.substr(8);
        }
        if (end == std::wstring::npos) break;
        position = end + 1;
    }
    return !request.empty() && request.size() <= 128 && std::all_of(request.begin(), request.end(), [](wchar_t ch) {
        return (ch >= L'a' && ch <= L'z') || (ch >= L'A' && ch <= L'Z') || (ch >= L'0' && ch <= L'9') || ch == L'-' || ch == L'_';
    });
}

void HandleActivationArguments(const std::vector<std::wstring>& arguments) {
    for (const auto& argument : arguments) {
        std::wstring fnId, request;
        if (!ParseAuthorizationUri(argument, fnId, request)) continue;
        if (g_authorizationRequest != request) {
            ++g_loginGeneration;
            CancelAuthorization(g_authorizationRequest);
            g_loginInFlight = false; g_busy = false;
            g_authorizationRequest = request;
            SetWindowTextW(g_passwordInput, L"");
            SetWindowTextW(g_usernameInput, g_username.c_str());
        }
        g_externalAuthorization = true;
        g_loginRequested = true; g_viewLogin = true;
        SetWindowTextW(g_fnIdInput, fnId.c_str());
        SetWindowTextW(g_loginError, L"");
        EnableWindow(g_loginButton, !g_loginInFlight);
        Layout(g_window); ShowMainWindow(g_window); SetFocus(g_usernameInput);
        return;
    }
    PostMessageW(g_window, kActivateWindow, 0, 0);
}

bool TransferPipeBytes(HANDLE pipe, void* buffer, DWORD size, bool write) {
    auto* bytes = static_cast<uint8_t*>(buffer);
    DWORD done = 0;
    while (done < size) {
        DWORD count = 0;
        const BOOL ok = write ? WriteFile(pipe, bytes + done, size - done, &count, nullptr) : ReadFile(pipe, bytes + done, size - done, &count, nullptr);
        if (!ok || count == 0) return false;
        done += count;
    }
    return true;
}

std::wstring ActivationPipeName(DWORD sessionId) { return L"\\\\.\\pipe\\FnCPN.UI.Activation." + std::to_wstring(sessionId); }

void ActivationServerLoop() {
    DWORD sessionId = 0; ProcessIdToSessionId(GetCurrentProcessId(), &sessionId);
    while (!g_closing) {
        PSECURITY_DESCRIPTOR descriptor{};
        std::wstring sid;
        HANDLE token{};
        if (OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) {
            DWORD size = 0; GetTokenInformation(token, TokenUser, nullptr, 0, &size);
            std::vector<uint8_t> storage(size);
            if (size && GetTokenInformation(token, TokenUser, storage.data(), size, &size)) {
                LPWSTR text{};
                if (ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(storage.data())->User.Sid, &text)) { sid = text; LocalFree(text); }
            }
            CloseHandle(token);
        }
        if (sid.empty()) return;
        const auto sddl = L"D:P(A;;GA;;;" + sid + L")";
        if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl.c_str(), SDDL_REVISION_1, &descriptor, nullptr)) return;
        SECURITY_ATTRIBUTES security{sizeof(security), descriptor, FALSE};
        HANDLE pipe = CreateNamedPipeW(ActivationPipeName(sessionId).c_str(), PIPE_ACCESS_INBOUND | FILE_FLAG_OVERLAPPED | FILE_FLAG_FIRST_PIPE_INSTANCE,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, 1, 0, 8192, 5000, &security);
        LocalFree(descriptor);
        if (pipe == INVALID_HANDLE_VALUE) return;
        PipeGuard pipeOwner{pipe};
        OVERLAPPED connection{}; connection.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if(!connection.hEvent) return;
        bool connected = ConnectNamedPipe(pipe, &connection) != FALSE;
        const DWORD connectError = connected ? ERROR_SUCCESS : GetLastError();
        if (connectError == ERROR_PIPE_CONNECTED) connected = true;
        else if (connectError == ERROR_IO_PENDING) {
            while (!g_closing && WaitForSingleObject(connection.hEvent, 250) == WAIT_TIMEOUT) {}
            DWORD transferred = 0;
            if (g_closing) CancelIoEx(pipe, &connection);
            connected = GetOverlappedResult(pipe, &connection, &transferred, TRUE) != FALSE;
        }
        CloseHandle(connection.hEvent);
        if (connected) {
            ULONG clientPid = 0; DWORD clientSession = 0;
            const bool sameSession = GetNamedPipeClientProcessId(pipe, &clientPid) && ProcessIdToSessionId(clientPid, &clientSession) && clientSession == sessionId;
            std::array<uint8_t, 4> header{};
            if (sameSession && TransferExact(pipe, header.data(), 4, false, 5000)) {
                const auto length = ReadU32(header);
                if (length > 0 && length <= 8192) {
                    std::vector<uint8_t> payload(length);
                    if (TransferExact(pipe, payload.data(), length, false, 5000)) {
                        try {
                            winrt::init_apartment(winrt::apartment_type::multi_threaded);
                            struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
                            const int charCount = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, reinterpret_cast<const char*>(payload.data()), static_cast<int>(payload.size()), nullptr, 0);
                            if (charCount <= 0) throw winrt::hresult_error(E_INVALIDARG);
                            std::wstring text(static_cast<size_t>(charCount), L'\0');
                            MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, reinterpret_cast<const char*>(payload.data()), static_cast<int>(payload.size()), text.data(), charCount);
                            auto array = JsonArray::Parse(text);
                            std::vector<std::wstring> args;
                            for (const auto& item : array) if (item.ValueType() == JsonValueType::String) args.emplace_back(item.GetString().c_str());
                            auto* forwarded = new std::vector<std::wstring>(std::move(args));
                            PostPayload(kActivationReceived, forwarded, 0);
                        } catch (...) {}
                    }
                }
            }
        }
        DisconnectNamedPipe(pipe);
    }
}

bool ForwardActivation(const std::vector<std::wstring>& arguments, DWORD sessionId) {
    JsonArray json;
    for (const auto& argument : arguments) json.Append(JsonValue::CreateStringValue(argument));
    const auto serialized = json.Stringify();
    const auto count = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, serialized.c_str(), -1, nullptr, 0, nullptr, nullptr);
    if (count <= 1 || count > 8193) return false;
    std::vector<uint8_t> payload(static_cast<size_t>(count));
    WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, serialized.c_str(), -1, reinterpret_cast<char*>(payload.data()), count, nullptr, nullptr);
    payload.resize(static_cast<size_t>(count - 1));
    std::array<uint8_t, 4> header{}; WriteU32(header, static_cast<uint32_t>(payload.size()));
    const auto deadline = GetTickCount64() + 5000;
    HANDLE pipe = INVALID_HANDLE_VALUE;
    while (GetTickCount64() < deadline) {
        pipe = CreateFileW(ActivationPipeName(sessionId).c_str(), GENERIC_WRITE, 0, nullptr, OPEN_EXISTING, FILE_FLAG_OVERLAPPED | SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION, nullptr);
        if (pipe != INVALID_HANDLE_VALUE) break;
        if (GetLastError() != ERROR_PIPE_BUSY) Sleep(100);
        else WaitNamedPipeW(ActivationPipeName(sessionId).c_str(), 250);
    }
    if (pipe == INVALID_HANDLE_VALUE) return false;
    if (!IsSameUserSessionServer(pipe)) { CloseHandle(pipe); return false; }
    const bool ok = TransferExact(pipe, header.data(), 4, true, 5000) && TransferExact(pipe, payload.data(), static_cast<DWORD>(payload.size()), true, 5000);
    CloseHandle(pipe); return ok;
}

bool IsValidProxyUrl(const std::wstring& value) {
    URL_COMPONENTS parts{sizeof(parts)};
    parts.dwUserNameLength = parts.dwPasswordLength = static_cast<DWORD>(-1);
    wchar_t host[256]{}, path[2048]{}, query[4096]{};
    parts.dwHostNameLength = static_cast<DWORD>(std::size(host)); parts.lpszHostName = host;
    parts.dwUrlPathLength = static_cast<DWORD>(std::size(path)); parts.lpszUrlPath = path;
    parts.dwExtraInfoLength = static_cast<DWORD>(std::size(query)); parts.lpszExtraInfo = query;
    if (!WinHttpCrackUrl(value.c_str(), static_cast<DWORD>(value.size()), 0, &parts)) return false;
    if (parts.nScheme != INTERNET_SCHEME_HTTP || parts.nPort == INTERNET_DEFAULT_HTTP_PORT || parts.dwUserNameLength || parts.dwPasswordLength) return false;
    std::wstring hostname(host, parts.dwHostNameLength);
    if (hostname != L"127.0.0.1" && hostname != L"::1" && hostname != L"[::1]") return false;
    const std::wstring pathname(path, parts.dwUrlPathLength);
    const std::wstring extra(query, parts.dwExtraInfoLength);
    if (pathname != L"/app/fncpn") return false;
    constexpr std::wstring_view prefix = L"?fncpn_proxy=";
    if (!extra.starts_with(prefix) || extra.size() != prefix.size() + 64) return false;
    const auto token = extra.substr(prefix.size());
    return std::all_of(token.begin(), token.end(), [](wchar_t ch) { return (ch >= L'0' && ch <= L'9') || (ch >= L'a' && ch <= L'f') || (ch >= L'A' && ch <= L'F'); });
}

bool IsAllowedAdminUrl(const std::wstring& value) {
    URL_COMPONENTS expected{sizeof(expected)}, actual{sizeof(actual)};
    actual.dwUserNameLength = actual.dwPasswordLength = static_cast<DWORD>(-1);
    actual.dwExtraInfoLength = static_cast<DWORD>(-1); // Keep query/fragment out of the URL path.
    wchar_t eHost[256]{}, aHost[256]{};
    expected.dwHostNameLength = static_cast<DWORD>(std::size(eHost)); expected.lpszHostName = eHost;
    actual.dwHostNameLength = static_cast<DWORD>(std::size(aHost)); actual.lpszHostName = aHost;
    wchar_t path[4096]{}; actual.lpszUrlPath = path; actual.dwUrlPathLength = static_cast<DWORD>(std::size(path));
    if (!WinHttpCrackUrl(g_adminUrl.c_str(), static_cast<DWORD>(g_adminUrl.size()), 0, &expected) ||
        !WinHttpCrackUrl(value.c_str(), static_cast<DWORD>(value.size()), 0, &actual)) return false;
    const std::wstring targetPath(path, actual.dwUrlPathLength);
    return actual.nScheme == INTERNET_SCHEME_HTTP && !actual.dwUserNameLength && !actual.dwPasswordLength &&
        (targetPath == L"/app/fncpn" || targetPath.starts_with(L"/app/fncpn/")) && expected.nPort == actual.nPort && std::wstring(eHost, expected.dwHostNameLength) == std::wstring(aHost, actual.dwHostNameLength);
}

std::wstring SanitizeExternalLink(const std::wstring& value) {
    URL_COMPONENTS parts{sizeof(parts)};
    parts.dwUserNameLength = parts.dwPasswordLength = static_cast<DWORD>(-1);
    wchar_t scheme[16]{}, host[256]{};
    parts.dwSchemeLength = static_cast<DWORD>(std::size(scheme)); parts.lpszScheme = scheme;
    parts.dwHostNameLength = static_cast<DWORD>(std::size(host)); parts.lpszHostName = host;
    if (!WinHttpCrackUrl(value.c_str(), static_cast<DWORD>(value.size()), 0, &parts) || parts.dwUserNameLength || parts.dwPasswordLength) return {};
    const std::wstring destination(host,parts.dwHostNameLength);
    if(destination==L"127.0.0.1" || destination==L"localhost" || destination==L"[::1]" || destination==L"::1") return {};
    if (parts.nScheme != INTERNET_SCHEME_HTTP && parts.nScheme != INTERNET_SCHEME_HTTPS) return {};
    auto safe = value;
    const auto query = value.find(L'?');
    if (query == std::wstring::npos) return safe;
    const auto fragment = value.find(L'#', query);
    const auto end = fragment == std::wstring::npos ? value.size() : fragment;
    safe.resize(query);
    bool first = true;
    size_t position = query + 1;
    while (position < end) {
        const auto separator = value.find(L'&', position);
        const auto next = separator == std::wstring::npos || separator > end ? end : separator;
        const auto field = value.substr(position, next - position);
        auto key = field.substr(0, field.find(L'='));
        if (!key.empty()) UrlUnescapeW(key.data(), nullptr, nullptr, URL_UNESCAPE_INPLACE);
        key.resize(wcslen(key.c_str()));
        if (_wcsicmp(key.c_str(), L"fncpn_proxy") != 0 && !field.empty()) {
            safe += first ? L"?" : L"&"; safe += field; first = false;
        }
        position = next + 1;
    }
    if (fragment != std::wstring::npos) safe += value.substr(fragment);
    return safe;
}

void OpenExternalLink(const std::wstring& value) {
    const auto safe = SanitizeExternalLink(value);
    if (!safe.empty()) ShellExecuteW(nullptr, L"open", safe.c_str(), nullptr, nullptr, SW_SHOWNORMAL);
}

LRESULT CALLBACK AdminWindowProc(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    if (message == WM_SIZE && g_webController) {
        RECT bounds{}; GetClientRect(window, &bounds); g_webController->put_Bounds(bounds); return 0;
    }
    if (message == WM_CLOSE) {
        ++g_adminGeneration;
        if (g_webController) g_webController->Close();
        g_webView2.Reset(); g_webView.Reset(); g_webController.Reset(); g_webEnvironment.Reset();
        g_adminWindow = nullptr; g_adminOpening = false;
        DestroyWindow(window); ShowMainWindow(g_window); return 0;
    }
    if (message == WM_DPICHANGED) {
        const auto* rect = reinterpret_cast<RECT*>(lParam);
        SetWindowPos(window, nullptr, rect->left, rect->top, rect->right - rect->left, rect->bottom - rect->top, SWP_NOZORDER | SWP_NOACTIVATE);
        return 0;
    }
    return DefWindowProcW(window, message, wParam, lParam);
}

RECT AdminWindowBounds(HWND owner) {
    MONITORINFO monitor{sizeof(monitor)};
    GetMonitorInfoW(MonitorFromWindow(owner,MONITOR_DEFAULTTONEAREST),&monitor);
    RECT anchor{}; GetWindowRect(owner,&anchor);
    // HTML main is capped at 1040 CSS pixels plus 32px side margins.
    RECT desired{0,0,Scale(owner,1080),Scale(owner,760)};
    AdjustWindowRectExForDpi(&desired,WS_OVERLAPPEDWINDOW,FALSE,WS_EX_APPWINDOW,GetDpiForWindow(owner));
    const int margin=Scale(owner,12);
    const int width=std::min(desired.right-desired.left,monitor.rcWork.right-monitor.rcWork.left-2*margin);
    const int height=std::min(desired.bottom-desired.top,monitor.rcWork.bottom-monitor.rcWork.top-2*margin);
    const int x=std::clamp(static_cast<int>((anchor.left+anchor.right-width)/2),static_cast<int>(monitor.rcWork.left)+margin,static_cast<int>(monitor.rcWork.right)-margin-width);
    const int y=std::clamp(static_cast<int>((anchor.top+anchor.bottom-height)/2),static_cast<int>(monitor.rcWork.top)+margin,static_cast<int>(monitor.rcWork.bottom)-margin-height);
    return RECT{x,y,x+width,y+height};
}

void ShowAdminWebView(HWND owner, const std::wstring& url) {
    if (!IsValidProxyUrl(url)) { MessageBoxW(owner, L("管理代理地址无效"), L"FnCPN", MB_OK | MB_ICONERROR); return; }
    g_adminUrl = url;
    URL_COMPONENTS origin{sizeof(origin)};
    wchar_t host[256]{}; origin.dwHostNameLength = static_cast<DWORD>(std::size(host)); origin.lpszHostName = host;
    if (WinHttpCrackUrl(url.c_str(), static_cast<DWORD>(url.size()), 0, &origin)) {
        g_adminOrigin = std::wstring(host, origin.dwHostNameLength) + L":" + std::to_wstring(origin.nPort);
    }
    if (!g_adminWindow) {
        const auto bounds=AdminWindowBounds(owner);
        g_adminWindow = CreateWindowExW(WS_EX_APPWINDOW, kAdminWindowClass, L("FnCPN 管理后台"), WS_OVERLAPPEDWINDOW,
            bounds.left,bounds.top,bounds.right-bounds.left,bounds.bottom-bounds.top, owner, nullptr, GetModuleHandleW(nullptr), nullptr);
    }
    if (!g_adminWindow) return;
    ShowWindow(g_adminWindow, SW_RESTORE);
    SetWindowPos(g_adminWindow,HWND_TOP,0,0,0,0,SWP_NOMOVE|SWP_NOSIZE);
    SetForegroundWindow(g_adminWindow);
    if (g_webView) { g_webView->Navigate(url.c_str()); return; }
    g_adminOpening = true;
    wchar_t local[MAX_PATH]{};
    if (SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA, nullptr, SHGFP_TYPE_CURRENT, local) != S_OK) return;
    const auto userData = std::wstring(local) + L"\\FnCPN\\WebView2";
    SHCreateDirectoryExW(nullptr, userData.c_str(), nullptr);
    const auto generation = ++g_adminGeneration;
    const auto hr = CreateCoreWebView2EnvironmentWithOptions(nullptr, userData.c_str(), nullptr,
        Microsoft::WRL::Callback<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler>(
            [url, generation, admin = g_adminWindow](HRESULT result, ICoreWebView2Environment* environment) -> HRESULT {
                if (g_closing || generation != g_adminGeneration || !IsWindow(admin) || admin != g_adminWindow) return S_OK;
                if (FAILED(result) || !environment) { g_adminOpening = false; MessageBoxW(admin, L("无法启动 WebView2 管理后台"), L"FnCPN", MB_OK | MB_ICONERROR); return S_OK; }
                g_webEnvironment = environment;
                return environment->CreateCoreWebView2Controller(g_adminWindow,
                    Microsoft::WRL::Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
                        [url, generation, admin](HRESULT controllerResult, ICoreWebView2Controller* controller) -> HRESULT {
                            if (g_closing || generation != g_adminGeneration || !IsWindow(admin) || admin != g_adminWindow) { if (controller) controller->Close(); return S_OK; }
                            g_adminOpening = false;
                            if (FAILED(controllerResult) || !controller) { MessageBoxW(admin, L("无法启动 WebView2 管理后台"), L"FnCPN", MB_OK | MB_ICONERROR); return S_OK; }
                            g_webController = controller;
                            const auto viewResult = controller->get_CoreWebView2(&g_webView);
                            if (FAILED(viewResult) || !g_webView) { controller->Close(); g_webController.Reset(); return FAILED(viewResult) ? viewResult : E_FAIL; }
                            RECT bounds{}; GetClientRect(g_adminWindow, &bounds); controller->put_Bounds(bounds);
                            g_webView->add_NavigationStarting(Microsoft::WRL::Callback<ICoreWebView2NavigationStartingEventHandler>(
                                [](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) -> HRESULT {
                                    LPWSTR raw{}; BOOL initiated = FALSE; args->get_Uri(&raw); args->get_IsUserInitiated(&initiated);
                                    const std::wstring target = raw ? raw : L""; CoTaskMemFree(raw);
                                    if (!IsAllowedAdminUrl(target)) { args->put_Cancel(TRUE); if (initiated) OpenExternalLink(target); }
                                    return S_OK;
                                }).Get(), &g_navigationToken);
                            g_webView->add_NewWindowRequested(Microsoft::WRL::Callback<ICoreWebView2NewWindowRequestedEventHandler>(
                                [](ICoreWebView2*, ICoreWebView2NewWindowRequestedEventArgs* args) -> HRESULT {
                                    LPWSTR raw{}; BOOL initiated = FALSE; args->get_Uri(&raw); args->get_IsUserInitiated(&initiated);
                                    const std::wstring target = raw ? raw : L""; CoTaskMemFree(raw); args->put_Handled(TRUE);
                                    if (IsAllowedAdminUrl(target)) g_webView->Navigate(target.c_str()); else if (initiated) OpenExternalLink(target);
                                    return S_OK;
                                }).Get(), &g_newWindowToken);
                            if (SUCCEEDED(g_webView.As(&g_webView2))) {
                                g_webView2->add_WebResourceResponseReceived(Microsoft::WRL::Callback<ICoreWebView2WebResourceResponseReceivedEventHandler>(
                                    [](ICoreWebView2*, ICoreWebView2WebResourceResponseReceivedEventArgs* args) -> HRESULT {
                                        Microsoft::WRL::ComPtr<ICoreWebView2WebResourceRequest> request;
                                        Microsoft::WRL::ComPtr<ICoreWebView2WebResourceResponseView> response;
                                        args->get_Request(&request); args->get_Response(&response);
                                        LPWSTR uri{}; int status = 0;
                                        if (request) request->get_Uri(&uri);
                                        if (response) response->get_StatusCode(&status);
                                        const bool local = uri && IsAllowedAdminUrl(uri); CoTaskMemFree(uri);
                                        if (local && (status == 401 || status == 403)) {
                                            g_operationError = L("登录已失效，请重新输入密码");
                                            SetWindowTextW(g_statusError, g_operationError.c_str());
                                            PostMessageW(g_adminWindow, WM_CLOSE, 0, 0);
                                            PostMessageW(g_window, WM_COMMAND, kRelogin, 0);
                                        }
                                        return S_OK;
                                    }).Get(), &g_responseToken);
                            }
                            g_webView->Navigate(url.c_str());
                            return S_OK;
                        }).Get());
            }).Get());
    if (FAILED(hr)) { g_adminOpening = false; MessageBoxW(owner, L("无法启动 WebView2 管理后台"), L"FnCPN", MB_OK | MB_ICONERROR); }
}

void ShowTrayMenu(HWND window) {
    HMENU menu = CreatePopupMenu();
    if (!menu) return;
    AppendMenuW(menu, MF_STRING, 1, L("打开 FnCPN"));
    AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
    AppendMenuW(menu, MF_STRING, 2, IsConnectedState(g_remoteState) ? L("断开连接") : (g_localWaiting ? L("继续远程连接") : L("连接")));
    AppendMenuW(menu, MF_STRING, 3, L("重新登录"));
    AppendMenuW(menu, MF_STRING, 4, L("重试"));
    AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
    AppendMenuW(menu, MF_STRING | (g_autoStart ? MF_CHECKED : 0), 5, L("开机启动"));
    AppendMenuW(menu, MF_STRING, 7, L("官网 · 捐赠"));
    AppendMenuW(menu, MF_STRING, 6, L("退出"));
    POINT point{};
    GetCursorPos(&point);
    SetForegroundWindow(window);
    const auto selected = TrackPopupMenu(menu, TPM_RETURNCMD | TPM_NONOTIFY | TPM_RIGHTBUTTON,
        point.x, point.y, 0, window, nullptr);
    DestroyMenu(menu);
    if (selected == 1) ShowMainWindow(window);
    if (selected == 2) PostMessageW(window, WM_COMMAND, kConnect, 0);
    if (selected == 3) PostMessageW(window, WM_COMMAND, kRelogin, 0);
    if (selected == 4) PostMessageW(window, WM_COMMAND, kRetry, 0);
    if (selected == 5) SetAutoStart(!g_autoStart);
    if (selected == 6) PostMessageW(window, WM_COMMAND, kExit, 0);
    if (selected == 7) ShellExecuteW(window, L"open", L"https://fncpn.rectcircle.cn/#donate", nullptr, nullptr, SW_SHOWNORMAL);
}

LRESULT CALLBACK WindowProc(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    if (g_taskbarCreated && message == g_taskbarCreated) {
        g_trayAdded = Shell_NotifyIconW(NIM_ADD, &g_tray) == TRUE;
        return 0;
    }
    switch (message) {
    case WM_CREATE: {
        g_window = window;
        g_tray.cbSize = sizeof(g_tray);
        g_tray.hWnd = window;
        g_tray.uID = 1;
        g_tray.uFlags = NIF_ICON | NIF_MESSAGE | NIF_TIP;
        g_tray.uCallbackMessage = kTrayCallback;
        g_tray.hIcon = static_cast<HICON>(LoadImageW(GetModuleHandleW(nullptr), MAKEINTRESOURCEW(1), IMAGE_ICON, 0, 0, LR_DEFAULTSIZE));
        wcscpy_s(g_tray.szTip, L"FnCPN");
        g_trayAdded = Shell_NotifyIconW(NIM_ADD, &g_tray) == TRUE;
        g_headerIcon = AddControl(L"STATIC", L"", SS_ICON, 1301);
        g_brand = AddControl(L"STATIC", L"FnCPN", SS_LEFT | SS_CENTERIMAGE, 1302);
        g_versionLabel = AddControl(L"STATIC", (std::wstring(L"v") + FNCPN_PRODUCT_VERSION).c_str(), SS_LEFT | SS_CENTERIMAGE, 1303);
        g_diagnosticsLabel = AddControl(L"STATIC", L("诊断详情"), SS_LEFT | SS_CENTERIMAGE, 1304);
        g_identity = AddControl(L"STATIC", L("尚未登录"), SS_LEFT | SS_CENTERIMAGE, kIdentity);
        g_state = AddControl(L"STATIC", L("正在读取状态"), SS_LEFT | SS_CENTERIMAGE, kState);
        g_connect = AddControl(L"BUTTON", L("连接"), BS_DEFPUSHBUTTON, kConnect);
        AddControl(L"BUTTON", L("刷新"), BS_PUSHBUTTON, kRefresh);
        AddControl(L"BUTTON", L("退出"), BS_PUSHBUTTON, kExit);
        g_retry = AddControl(L"BUTTON", L("重新检测"), BS_PUSHBUTTON, kRetry);
        g_relogin = AddControl(L"BUTTON", L("重新登录"), BS_PUSHBUTTON, kRelogin);
        g_changeServer = AddControl(L"BUTTON", L("更换 NAS"), BS_PUSHBUTTON, kChangeServer);
        g_admin = AddControl(L"BUTTON", L("管理后台"), BS_PUSHBUTTON, kAdmin);
        g_dnsLabel = AddControl(L"STATIC", L"DNS", SS_LEFT | SS_CENTERIMAGE, 0);
        g_dnsValue = AddControl(L"STATIC", L"-", SS_LEFT | SS_CENTERIMAGE | SS_ENDELLIPSIS, 0);
        g_copyDNSSuffix = AddControl(L"BUTTON", L("复制 DNS 后缀"), BS_PUSHBUTTON, kCopyDNSSuffix);
        g_copyDiagnostics = AddControl(L"BUTTON", L("复制"), BS_PUSHBUTTON, kCopyDiagnostics);
        g_openLog = AddControl(L"BUTTON", L("日志"), BS_PUSHBUTTON, kOpenLog);
        g_openNas = AddControl(L"BUTTON", L"", BS_PUSHBUTTON | BS_ICON, kOpenNas);
        SetWindowTextW(g_openNas, L("打开 NAS")); // Accessible name retained for the icon-only button.
        g_autostart = AddControl(L"BUTTON", L("开机启动"), BS_AUTOCHECKBOX, kAutostart);
        g_repair = AddControl(L"BUTTON", L("修复特权服务…"), BS_PUSHBUTTON, kRepair);
        g_statusError = AddControl(L"STATIC", L"", SS_LEFT | SS_CENTERIMAGE, kStatusError);
        g_diagnosticValue = AddControl(L"EDIT", L"", WS_BORDER | ES_MULTILINE | ES_READONLY | ES_AUTOVSCROLL | WS_VSCROLL, kDiagnosticValue);
        g_fnIdLabel = AddControl(L"STATIC", L"FN Connect ID", SS_LEFT | SS_CENTERIMAGE, kFnIdLabel);
        g_fnIdInput = AddControl(L"EDIT", L"", WS_BORDER | ES_AUTOHSCROLL, kFnId);
        g_usernameLabel = AddControl(L"STATIC", L("fnOS 用户名"), SS_LEFT | SS_CENTERIMAGE, kUsernameLabel);
        g_usernameInput = AddControl(L"EDIT", L"", WS_BORDER | ES_AUTOHSCROLL, kUsername);
        g_passwordLabel = AddControl(L"STATIC", L("fnOS 密码"), SS_LEFT | SS_CENTERIMAGE, kPasswordLabel);
        g_passwordInput = AddControl(L"EDIT", L"", WS_BORDER | ES_PASSWORD | ES_AUTOHSCROLL, kPassword);
        g_loginError = AddControl(L"STATIC", L"", SS_LEFT | SS_CENTERIMAGE, kLoginError);
        g_loginButton = AddControl(L"BUTTON", L("登录并连接"), BS_DEFPUSHBUTTON, kLogin);
        g_loginReturn = AddControl(L"BUTTON", L("返回详情"), BS_PUSHBUTTON, kLoginReturn);
        g_loginCancel = AddControl(L"BUTTON", L("取消"), BS_PUSHBUTTON, kLoginCancel);
        static const wchar_t* rowNames[] = {L("连接路径"), L("隧道地址"), L("网络接口"), L("最近握手"), L("特权服务"), L("IPv6 检测"), L"NAS IP"};
        for (int i = 0; i < 7; ++i) {
            g_fieldLabels[i] = AddControl(L"STATIC", L(rowNames[i]).c_str(), SS_LEFT | SS_CENTERIMAGE, 1100 + i);
            g_values[i] = AddControl(L"STATIC", L"-", SS_LEFT | SS_CENTERIMAGE, 1200 + i);
        }
        g_autoStart = false;
        HKEY runKey{};
        if (RegOpenKeyExW(HKEY_CURRENT_USER, L"Software\\Microsoft\\Windows\\CurrentVersion\\Run", 0, KEY_QUERY_VALUE, &runKey) == ERROR_SUCCESS) {
            DWORD size = 0, type = 0;
            if (RegQueryValueExW(runKey, L"FnCPN", nullptr, &type, nullptr, &size) == ERROR_SUCCESS && type == REG_SZ) g_autoStart = true;
            RegCloseKey(runKey);
        }
        SendMessageW(g_autostart, BM_SETCHECK, g_autoStart ? BST_CHECKED : BST_UNCHECKED, 0);
        SendMessageW(g_identity, WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE);
        SendMessageW(g_state, WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE);
        for (int id : {kConnect, kRefresh, kExit, kRetry, kRelogin, kChangeServer, kAdmin, kCopyDiagnostics, kCopyDNSSuffix, kOpenLog, kOpenNas, kAutostart, kRepair, kStatusError, kDiagnosticValue, kLoginReturn, kLoginCancel}) SendMessageW(GetDlgItem(window, id), WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE);
        for (int id : {kFnIdLabel, kFnId, kUsernameLabel, kUsername, kPasswordLabel, kPassword, kLoginError, kLogin})
            SendMessageW(GetDlgItem(window, id), WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE);
        for (int i = 0; i < 7; ++i) { SendMessageW(g_values[i], WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE); SendMessageW(g_fieldLabels[i], WM_SETFONT, reinterpret_cast<WPARAM>(GetStockObject(DEFAULT_GUI_FONT)), TRUE); }
        SendMessageW(g_passwordInput, EM_SETPASSWORDCHAR, L'●', 0);
        UpdateFont(window);
#ifndef FNCPN_NATIVE_TEST
        if (g_autoStart) SetAutoStart(true); // Keep the existing preference pointing at the renamed UI.
#endif
        SetTimer(window, kRefreshTimer, 3000, nullptr);
        g_busy = true;
#ifndef FNCPN_NATIVE_TEST
        StartWorker(EnsureDaemonFromWorker);
#endif
        return 0;
    }
    case WM_GETMINMAXINFO: {
        auto* info = reinterpret_cast<MINMAXINFO*>(lParam);
        info->ptMinTrackSize = {Scale(window, 610), Scale(window, 540)};
        return 0;
    }
    case WM_ERASEBKGND: {
        RECT area{}; GetClientRect(window, &area);
        FillRect(reinterpret_cast<HDC>(wParam), &area, g_backgroundBrush ? g_backgroundBrush : GetSysColorBrush(COLOR_BTNFACE)); return 1;
    }
    case WM_CTLCOLORSTATIC:
    case WM_CTLCOLORBTN: {
        HDC dc = reinterpret_cast<HDC>(wParam); HWND control = reinterpret_cast<HWND>(lParam);
        for(HWND input : {g_fnIdInput,g_usernameInput,g_passwordInput}) if(input && control==GetPropW(input,L"InputFrame")) {
            SetBkMode(dc,OPAQUE); SetBkColor(dc,GetSysColor(COLOR_WINDOW));
            return reinterpret_cast<LRESULT>(GetSysColorBrush(COLOR_WINDOW));
        }
        SetBkMode(dc, TRANSPARENT); SetBkColor(dc, g_backgroundColor);
        COLORREF color = GetSysColor(COLOR_WINDOWTEXT);
        if (!g_highContrast) {
            color = RGB(35, 38, 43);
            if (control == g_versionLabel || control == g_diagnosticsLabel || control == g_dnsLabel || std::find(std::begin(g_fieldLabels), std::end(g_fieldLabels), control) != std::end(g_fieldLabels)) color = RGB(112, 117, 125);
            if (control == g_state) color = (g_remoteState == L"LOCAL" || g_remoteState == L"DIRECT" || g_remoteState == L"RELAY") ? RGB(35, 164, 82) : RGB(112, 117, 125);
            if (control == g_statusError || control == g_loginError) color = RGB(181, 49, 49);
        }
        if (control == g_diagnosticValue) { SetBkColor(dc, GetSysColor(COLOR_WINDOW)); SetTextColor(dc, GetSysColor(COLOR_WINDOWTEXT)); return reinterpret_cast<LRESULT>(GetSysColorBrush(COLOR_WINDOW)); }
        SetTextColor(dc, color); return reinterpret_cast<LRESULT>(g_backgroundBrush);
    }
    case WM_SETTINGCHANGE: UpdateFont(window); Layout(window); return 0;
    case WM_SIZE: Layout(window); return 0;
    case WM_DRAWITEM: {
        const auto* item = reinterpret_cast<DRAWITEMSTRUCT*>(lParam);
        if (item->CtlType != ODT_BUTTON) break;
        if(item->CtlID==kOpenNas || item->CtlID==kCopyDNSSuffix) {
            FillRect(item->hDC,&item->rcItem,g_backgroundBrush);
            const int size=Scale(window,16);
            DrawIconEx(item->hDC,(item->rcItem.right-size)/2,(item->rcItem.bottom-size)/2,item->CtlID == kOpenNas ? g_nasButtonIcon : g_dnsButtonIcon,size,size,0,nullptr,DI_NORMAL);
            return TRUE;
        }
        auto theme = OpenThemeData(window, L"Button");
        POINT pointer{}; GetCursorPos(&pointer); ScreenToClient(item->hwndItem, &pointer);
        const bool hovered = PtInRect(&item->rcItem, pointer);
        int state = (item->itemState & ODS_DISABLED) ? PBS_DISABLED : (item->itemState & ODS_SELECTED) ? PBS_PRESSED : hovered ? PBS_HOT : (item->itemState & ODS_FOCUS) ? PBS_DEFAULTED : PBS_NORMAL;
        if (theme) { DrawThemeBackground(theme, item->hDC, BP_PUSHBUTTON, state, &item->rcItem, nullptr); CloseThemeData(theme); }
        else DrawFrameControl(item->hDC, const_cast<RECT*>(&item->rcItem), DFC_BUTTON, DFCS_BUTTONPUSH | ((item->itemState & ODS_SELECTED) ? DFCS_PUSHED : 0));
        auto previous = SelectObject(item->hDC, g_font); auto text = GetText(item->hwndItem);
        SIZE extent{}; GetTextExtentPoint32W(item->hDC, text.c_str(), static_cast<int>(text.size()), &extent);
        const int iconSize = Scale(window,16), gap = Scale(window,6);
        BUTTON_IMAGELIST image{}; SendMessageW(item->hwndItem, BCM_GETIMAGELIST, 0, reinterpret_cast<LPARAM>(&image));
        int x = (item->rcItem.right - extent.cx - (image.himl ? iconSize + gap : 0)) / 2;
        if (image.himl) {
            const int top = (item->rcItem.bottom - iconSize)/2;
            if (item->CtlID == kConnect) {
                auto iconFont = CreateFontW(-iconSize,0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,ANTIALIASED_QUALITY,DEFAULT_PITCH,L"Segoe MDL2 Assets");
                SelectObject(item->hDC,iconFont); RECT iconRect{x,top,x+iconSize,top+iconSize};
                const wchar_t glyph = 0xE945;
                SetBkMode(item->hDC,TRANSPARENT); SetTextColor(item->hDC,GetSysColor((item->itemState & ODS_DISABLED) ? COLOR_GRAYTEXT : COLOR_BTNTEXT));
                if (IsConnectedState(g_remoteState)) {
                    InflateRect(&iconRect,-Scale(window,3),-Scale(window,3));
                    auto brush=CreateSolidBrush(GetSysColor((item->itemState & ODS_DISABLED) ? COLOR_GRAYTEXT : COLOR_BTNTEXT));
                    FillRect(item->hDC,&iconRect,brush); DeleteObject(brush);
                } else DrawTextW(item->hDC,&glyph,1,&iconRect,DT_CENTER|DT_VCENTER|DT_SINGLELINE);
                SelectObject(item->hDC,g_font); DeleteObject(iconFont);
            } else ImageList_Draw(image.himl,0,item->hDC,x,top,ILD_TRANSPARENT);
            x += iconSize + gap;
        }
        RECT textRect{x,0,x+extent.cx,item->rcItem.bottom}; SetBkMode(item->hDC,TRANSPARENT);
        SetTextColor(item->hDC,GetSysColor((item->itemState & ODS_DISABLED) ? COLOR_GRAYTEXT : COLOR_BTNTEXT));
        DrawTextW(item->hDC,text.c_str(),static_cast<int>(text.size()),&textRect,DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX);
        SelectObject(item->hDC,previous); return TRUE;
    }
    case WM_DPICHANGED: {
        UpdateFont(window);
        const auto* suggested = reinterpret_cast<const RECT*>(lParam);
        SetWindowPos(window, nullptr, suggested->left, suggested->top, suggested->right - suggested->left,
            suggested->bottom - suggested->top, SWP_NOZORDER | SWP_NOACTIVATE);
        Layout(window);
        if (g_webController && g_adminWindow) { RECT bounds{}; GetClientRect(g_adminWindow, &bounds); g_webController->put_Bounds(bounds); }
        return 0;
    }
    case WM_VSCROLL: {
        SCROLLINFO info{sizeof(info), SIF_ALL}; GetScrollInfo(window, SB_VERT, &info);
        int position = info.nPos;
        switch (LOWORD(wParam)) {
        case SB_LINEUP: position -= 32; break;
        case SB_LINEDOWN: position += 32; break;
        case SB_PAGEUP: position -= static_cast<int>(info.nPage); break;
        case SB_PAGEDOWN: position += static_cast<int>(info.nPage); break;
        case SB_THUMBTRACK: position = info.nTrackPos; break;
        default: break;
        }
        info.fMask = SIF_POS; info.nPos = std::max(info.nMin, std::min(position, info.nMax - static_cast<int>(info.nPage) + 1));
        SetScrollInfo(window, SB_VERT, &info, TRUE); g_scrollY = info.nPos; Layout(window); return 0;
    }
    case WM_MOUSEWHEEL: {
        const int delta = GET_WHEEL_DELTA_WPARAM(wParam);
        SendMessageW(window, WM_VSCROLL, MAKELONG(delta > 0 ? SB_LINEUP : SB_LINEDOWN, 0), 0); return 0;
    }
    case WM_TIMER:
        if (wParam == kRefreshTimer) StartRefresh();
        return 0;
    case kStatusReady: {
        auto refreshed = TakePayload<RefreshResult>(message,wParam);
        if(!refreshed) return 0;
        g_busy = false;
        g_remoteState = refreshed->state;
        g_localWaiting = refreshed->localWaiting;
        if (g_localWaiting) refreshed->label = L("正在等待局域网访问权限");
        g_configured = !refreshed->fnId.empty();
        g_privilegedAvailable = refreshed->privilegedAvailable;
        g_administrator = refreshed->administrator;
        if (g_adminWindow && (!g_administrator || (!g_fnId.empty() && g_fnId != refreshed->fnId) || refreshed->state == L"AUTH_REQUIRED"))
            PostMessageW(g_adminWindow, WM_CLOSE, 0, 0);
        g_username = refreshed->username;
        if (GetText(g_usernameInput).empty() && !g_username.empty()) SetWindowTextW(g_usernameInput, g_username.c_str());
        if (!refreshed->diagnosticJson.empty()) {
            try { g_diagnostics = JsonObject::Parse(refreshed->diagnosticJson); } catch (...) { g_diagnostics = JsonObject{}; }
        }
        const std::array<std::wstring, 7> values{refreshed->path, refreshed->address, refreshed->network, refreshed->handshake, refreshed->privilege, refreshed->direct, refreshed->nas};
        for (size_t i = 0; i < values.size(); ++i) if (g_values[i]) SetWindowTextW(g_values[i], values[i].c_str());
        g_dnsSuffix = refreshed->dnsSuffix;
        SetWindowTextW(g_dnsValue, refreshed->dnsText.c_str());
        g_diagnosticBase=refreshed->details;
        if (IsConnectedState(refreshed->state)) { g_operationError.clear(); refreshed->statusError.clear(); }
        if (g_statusError) SetWindowTextW(g_statusError, (g_operationError.empty() ? refreshed->statusError : g_operationError).c_str());
        if (g_configured && !g_loginRequested && !g_externalAuthorization) g_fnId = refreshed->fnId;
        g_viewLogin = g_loginRequested || g_loginInFlight || g_externalAuthorization || !g_configured || g_remoteState == L"AUTH_REQUIRED";
        Layout(g_window);
        SetStatus(refreshed->label, g_configured ? L"FnCPN · " + g_fnId : L"FnCPN");
        if (g_viewLogin && g_configured && !g_loginRequested && !g_loginInFlight && !g_externalAuthorization) {
            SetWindowTextW(g_fnIdInput, g_fnId.c_str());
            SetWindowTextW(g_loginError, refreshed->label.c_str());
        }
        return 0;
    }
    case kWorkerFailed:
        g_busy=false; g_loginInFlight=false; g_operationInFlight=false; g_adminOpening=false;
        SetWindowTextW(g_statusError,L("FnCPN 请求失败")); Layout(window); return 0;
    case kStatusChanged:
        StartRefresh(); return 0;
    case kDaemonReady:
        if (!g_watchStarted) { g_watchStarted = true; StartWorker(WatchStatusFromWorker); }
        g_busy = false;
        StartRefresh();
        return 0;
    case kLoginResult: {
        auto loginMessage = TakePayload<std::wstring>(message,wParam);
        if(!loginMessage) return 0;
        if (!g_loginInFlight || static_cast<unsigned>(lParam) != g_loginGeneration) return 0;
        g_busy = false;
        EnableWindow(g_loginButton, TRUE);
        if (loginMessage->empty()) {
            g_fnId = NormalizeFnId(GetText(g_fnIdInput));
            g_configured = true;
            g_loginRequested = false; g_externalAuthorization = false;
            g_viewLogin = false;
            Layout(g_window);
            SetStatus(L("登录成功，正在读取状态"), L"FnCPN · " + g_fnId);
            StartRefresh();
        } else {
            SetWindowTextW(g_loginError, loginMessage->c_str());
        }
        g_loginInFlight = false;
        g_authorizationRequest.clear();
        Layout(window);
        return 0;
    }
    case kAuthorizationStarted: {
        auto request = TakePayload<std::wstring>(message,wParam);
        if(!request) return 0;
        if (!g_loginInFlight || static_cast<unsigned>(lParam) != g_loginGeneration) { CancelAuthorization(*request); return 0; }
        g_authorizationRequest = *request;
        if (g_loginCancel) ShowWindow(g_loginCancel, SW_SHOW);
        return 0;
    }
    case kOperationReady: {
        auto error = TakePayload<std::wstring>(message,wParam);
        if(!error) return 0;
        g_operationInFlight = false;
        g_busy = false;
        if (!error->empty()) { g_operationError = *error; if (g_statusError) SetWindowTextW(g_statusError, error->c_str()); }
        StartRefresh();
        return 0;
    }
    case kExitReady:
        g_allowExit = true;
        DestroyWindow(window);
        return 0;
    case kAdminReady: {
        auto data = TakePayload<std::pair<std::wstring, std::wstring>>(message,wParam);
        if(!data) return 0;
        g_adminOpening = false;
        if (!data->second.empty()) { g_adminError=data->second; }
        else ShowAdminWebView(window, data->first);
        Layout(window);
        return 0;
    }
    case kActivationReceived: {
        auto arguments = TakePayload<std::vector<std::wstring>>(message,wParam);
        if(!arguments) return 0;
        HandleActivationArguments(*arguments);
        return 0;
    }
    case kActivateWindow:
        ShowMainWindow(window);
        return 0;
    case kTrayCallback:
        if (lParam == WM_LBUTTONDBLCLK || lParam == WM_LBUTTONUP) ShowMainWindow(window);
        else if (lParam == WM_RBUTTONUP || lParam == WM_CONTEXTMENU) ShowTrayMenu(window);
        return 0;
    case WM_COMMAND:
        if (LOWORD(wParam) == IDOK && g_viewLogin) { SendMessageW(window, WM_COMMAND, kLogin, 0); return 0; }
        if (LOWORD(wParam) == IDCANCEL) { SendMessageW(window, WM_CLOSE, 0, 0); return 0; }
        switch (LOWORD(wParam)) {
        case kRefresh: StartRefresh(); return 0;
        case kConnect:
            if (!g_configured || g_remoteState == L"AUTH_REQUIRED") { g_viewLogin = true; Layout(window); SetFocus(g_usernameInput); return 0; }
            if (g_localWaiting) RunIpcOperation(L"continue-remote", L("正在继续远程连接…"));
            else if (IsConnectedState(g_remoteState)) RunIpcOperation(L"disconnect", L("正在断开连接…"));
            else RunIpcOperation(L"connect", L("正在连接…"));
            return 0;
        case kLogin: {
            if (g_loginInFlight || g_operationInFlight) return 0;
            const auto fnId = NormalizeFnId(GetText(g_fnIdInput));
            const auto username = GetText(g_usernameInput);
            const auto password = GetText(g_passwordInput);
            if (fnId.empty()) { SetWindowTextW(g_loginError, L("FN Connect ID 格式不正确")); SetFocus(g_fnIdInput); return 0; }
            if (username.empty()) { SetWindowTextW(g_loginError, L("请输入 fnOS 用户名")); SetFocus(g_usernameInput); return 0; }
            if (password.empty()) { SetWindowTextW(g_loginError, L("请输入 fnOS 密码")); SetFocus(g_passwordInput); return 0; }
            SetWindowTextW(g_fnIdInput, fnId.c_str());
            SetWindowTextW(g_passwordInput, L"");
            SetWindowTextW(g_loginError, L("正在登录并连接…"));
            g_busy = true;
            g_loginInFlight = true;
            EnableWindow(g_loginButton, FALSE);
            Layout(window);
            StartWorker(LoginFromWorker, fnId, username, password, g_authorizationRequest, g_externalAuthorization, ++g_loginGeneration);
            return 0;
        }
        case kExit:
            if (g_exitStarted) return 0;
            g_exitStarted = true;
            CancelAuthorization(g_authorizationRequest);
            g_busy = true;
            StartWorker([] {
                winrt::init_apartment(winrt::apartment_type::multi_threaded);
                struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
                try { (void)CallIpc(L"shutdown-client-daemon"); }
                catch (...) {}
                PostMessageW(g_window, kExitReady, 0, 0);
            });
            return 0;
        case kRetry: RunIpcOperation(L"retry", L("正在重试连接…")); return 0;
        case kRelogin: g_viewLogin = true; g_loginRequested = true; SetWindowTextW(g_fnIdInput,g_fnId.c_str()); SetWindowTextW(g_identity,L("重新登录")); SetWindowTextW(g_passwordInput, L""); SetWindowTextW(g_loginError, L""); Layout(window); SetFocus(g_usernameInput); return 0;
        case kChangeServer: g_loginRequested = true; g_viewLogin = true; SetWindowTextW(g_fnIdInput, L""); SetWindowTextW(g_usernameInput, L""); SetWindowTextW(g_passwordInput, L""); SetWindowTextW(g_loginError, L""); Layout(window); SetFocus(g_fnIdInput); return 0;
        case kLoginReturn:
        case kLoginCancel:
            if (g_loginInFlight || g_externalAuthorization) {
                ++g_loginGeneration;
                CancelAuthorization(g_authorizationRequest);
                g_loginInFlight = false; g_busy = false; g_authorizationRequest.clear();
                SetWindowTextW(g_loginError, L("登录已取消"));
            }
            g_loginRequested = false; g_externalAuthorization = false;
            g_viewLogin = false; SetWindowTextW(g_passwordInput, L""); Layout(window); return 0;
        case kCopyDiagnostics: CopyDiagnostics(window); return 0;
        case kCopyDNSSuffix: if (!g_dnsSuffix.empty()) CopyText(window, g_dnsSuffix); return 0;
        case kOpenLog: OpenLogs(); return 0;
        case kOpenNas: OpenNas(); return 0;
        case kAutostart: SetAutoStart(SendMessageW(g_autostart, BM_GETCHECK, 0, 0) == BST_CHECKED); return 0;
        case kRepair: {
            if (g_operationInFlight) return 0;
            g_operationInFlight = true; g_busy = true; g_operationError.clear();
            StartWorker(RepairServiceFromWorker); return 0;
        }
        case kAdmin: {
            if (!g_administrator || g_adminOpening) return 0;
            g_adminOpening = true;
            g_adminError.clear();
            StartWorker([] {
                winrt::init_apartment(winrt::apartment_type::multi_threaded);
                struct ApartmentGuard { ~ApartmentGuard() { winrt::uninit_apartment(); } } apartmentGuard;
                std::wstring url, error;
                try { url = JsonString(CallIpc(L"admin-proxy").GetNamedObject(L"result"), L"url"); }
                catch (const winrt::hresult_error& failure) { error = failure.message().c_str(); }
                auto* data = new std::pair<std::wstring, std::wstring>(std::move(url), std::move(error));
                PostPayload(kAdminReady, data, 0);
            });
            return 0;
        }
        }
        break;
    case WM_QUERYENDSESSION: return TRUE;
    case WM_ENDSESSION:
        // Restart Manager needs the executable released before this handler returns.
        // The installer owns daemon shutdown; do not change connection intent here.
        if (wParam) { g_exitStarted = true; DestroyWindow(window); }
        return 0;
    case WM_CLOSE:
        ShowWindow(window, SW_HIDE);
        return 0;
    case WM_DESTROY:
        ClosePayloadQueue();
        ++g_adminGeneration;
        CancelWorkerIO();
        KillTimer(window, kRefreshTimer);
        if (g_webController) g_webController->Close();
        g_webView2.Reset(); g_webView.Reset(); g_webController.Reset(); g_webEnvironment.Reset();
        for (auto images : g_buttonImages) ImageList_Destroy(images);
        g_buttonImages.clear();
        if (g_nasButtonIcon) { DestroyIcon(g_nasButtonIcon); g_nasButtonIcon = nullptr; }
        if (g_dnsButtonIcon) { DestroyIcon(g_dnsButtonIcon); g_dnsButtonIcon = nullptr; }
        for (HFONT font : {g_font, g_titleFont, g_deviceFont, g_statusFont, g_monoFont}) if (font) DeleteObject(font);
        g_font = g_titleFont = g_deviceFont = g_statusFont = g_monoFont = nullptr;
        if (g_backgroundBrush) { DeleteObject(g_backgroundBrush); g_backgroundBrush = nullptr; }
        if (g_headerImage) { DestroyIcon(g_headerImage); g_headerImage = nullptr; }
        if (g_trayAdded) Shell_NotifyIconW(NIM_DELETE, &g_tray);
        if (g_tray.hIcon) DestroyIcon(g_tray.hIcon);
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcW(window, message, wParam, lParam);
}
}

#ifndef FNCPN_NATIVE_TEST
int WINAPI wWinMain(_In_ HINSTANCE instance, _In_opt_ HINSTANCE, _In_ PWSTR, _In_ int show) {
    winrt::init_apartment(winrt::apartment_type::single_threaded);
    int argumentCount = 0;
    LPWSTR* argumentVector = CommandLineToArgvW(GetCommandLineW(), &argumentCount);
    std::vector<std::wstring> arguments;
    bool background = false;
    for (int i = 1; i < argumentCount; ++i) {
        if (wcscmp(argumentVector[i], L"--background") == 0) background = true;
        else arguments.emplace_back(argumentVector[i]);
    }
    if (argumentVector) LocalFree(argumentVector);
    DWORD sessionId = 0;
    ProcessIdToSessionId(GetCurrentProcessId(), &sessionId);
    HANDLE token{};
    std::wstring userSid = L"unknown";
    if (OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) {
        DWORD required = 0;
        GetTokenInformation(token, TokenUser, nullptr, 0, &required);
        std::vector<uint8_t> tokenUserBuffer(required);
        if (required && GetTokenInformation(token, TokenUser, tokenUserBuffer.data(), required, &required)) {
            auto* tokenUser = reinterpret_cast<TOKEN_USER*>(tokenUserBuffer.data());
            LPWSTR sidText = nullptr;
            if (ConvertSidToStringSidW(tokenUser->User.Sid, &sidText)) {
                userSid = sidText;
                LocalFree(sidText);
            }
        }
        CloseHandle(token);
    }
    const auto mutexName = L"Local\\FnCPN.UI." + userSid + L"." + std::to_wstring(sessionId);
    g_singleInstance = CreateMutexW(nullptr, TRUE, mutexName.c_str());
    if (!g_singleInstance || GetLastError() == ERROR_ALREADY_EXISTS) {
        if (!ForwardActivation(arguments, sessionId)) MessageBoxW(nullptr, L("FnCPN 已在运行，但无法将请求转交给现有窗口。请检查应用状态后重试。"), L"FnCPN", MB_OK | MB_ICONWARNING);
        if (g_singleInstance) CloseHandle(g_singleInstance);
        return 0;
    }
    INITCOMMONCONTROLSEX controls{sizeof(controls), ICC_STANDARD_CLASSES};
    InitCommonControlsEx(&controls);
    WNDCLASSEXW wc{sizeof(wc)};
    wc.lpfnWndProc = WindowProc;
    wc.hInstance = instance;
    wc.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    wc.hIcon = LoadIconW(instance, MAKEINTRESOURCEW(1));
    wc.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    wc.lpszClassName = kWindowClass;
    RegisterClassExW(&wc);
    WNDCLASSEXW adminClass{sizeof(adminClass)};
    adminClass.lpfnWndProc = AdminWindowProc;
    adminClass.hInstance = instance;
    adminClass.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    adminClass.hIcon = LoadIconW(instance, MAKEINTRESOURCEW(1));
    adminClass.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    adminClass.lpszClassName = kAdminWindowClass;
    RegisterClassExW(&adminClass);
    g_taskbarCreated = RegisterWindowMessageW(L"TaskbarCreated");
    HWND window = CreateWindowExW(0, kWindowClass, L"FnCPN", WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN,
        CW_USEDEFAULT, CW_USEDEFAULT, 610, 760, nullptr, nullptr, instance, nullptr);
    if (!window) { ReleaseMutex(g_singleInstance); CloseHandle(g_singleInstance); return 1; }
    RECT desired{0, 0, Scale(window, 600), Scale(window, 692)};
    AdjustWindowRectExForDpi(&desired, WS_OVERLAPPEDWINDOW, FALSE, 0, GetDpiForWindow(window));
    MONITORINFO monitor{sizeof(monitor)}; GetMonitorInfoW(MonitorFromWindow(window,MONITOR_DEFAULTTONEAREST),&monitor);
    const RECT work=monitor.rcWork;
    const int windowWidth=std::min(desired.right-desired.left,work.right-work.left-Scale(window,24));
    const int windowHeight=std::min(desired.bottom-desired.top,work.bottom-work.top-Scale(window,24));
    SetWindowPos(window, nullptr, work.left+(work.right-work.left-windowWidth)/2, work.top+(work.bottom-work.top-windowHeight)/2,
        windowWidth,windowHeight,SWP_NOZORDER);
    g_startInBackground = background;
    ShowWindow(window, background ? SW_HIDE : show);
    UpdateWindow(window);
    StartWorker(ActivationServerLoop);
    if (!arguments.empty()) {
        auto* initial = new std::vector<std::wstring>(std::move(arguments));
    PostPayload(kActivationReceived,initial);
    }
    MSG message{};
    while (GetMessageW(&message, nullptr, 0, 0) > 0) {
        if (IsDialogMessageW(window, &message)) continue;
        TranslateMessage(&message);
        DispatchMessageW(&message);
    }
    JoinWorkers();
    ReleaseMutex(g_singleInstance);
    CloseHandle(g_singleInstance);
    return static_cast<int>(message.wParam);
}

#endif
