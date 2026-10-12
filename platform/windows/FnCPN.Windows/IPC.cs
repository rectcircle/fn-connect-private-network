using System.Buffers.Binary;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Reflection;
using System.Security.Principal;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace FnCPN.Windows;

internal static class IPC
{
    internal const int ProtocolVersion = 4;
    private static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web);
    private static readonly string PipeName = $"FnCPN.Client.v1.{Process.GetCurrentProcess().SessionId}";

    public static async Task<JsonElement> CallAsync(string method, object? parameters = null, CancellationToken cancellationToken = default)
    {
        using var timeout = new CancellationTokenSource(TimeoutForMethod(method));
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, timeout.Token);
        try
        {
            var requestToken = linked.Token;
            await using var pipe = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Identification);
            await pipe.ConnectAsync(5000, requestToken);
            var requestId = Guid.NewGuid().ToString("N");
            var request = JsonSerializer.SerializeToUtf8Bytes(new Request(ProtocolVersion, requestId, method, parameters), Json);
            await WriteFrameAsync(pipe, request, requestToken);
            using var responseDocument = await ReadFrameAsync(pipe, requestToken);
            var root = responseDocument.RootElement;
            ValidateResponseEnvelope(root, requestId);
            if (!root.GetProperty("ok").GetBoolean())
            {
                var error = root.GetProperty("error");
                throw new IPCException(error.GetProperty("message").GetString()!, error.Clone());
            }
            return root.TryGetProperty("result", out var result) ? result.Clone() : JsonDocument.Parse("null").RootElement.Clone();
        }
        catch (OperationCanceledException exception) when (!cancellationToken.IsCancellationRequested && timeout.IsCancellationRequested)
        {
            throw new TimeoutException($"Go daemon request '{method}' exceeded its {TimeoutForMethod(method).TotalSeconds:0}-second time limit", exception);
        }
    }

    internal static void ValidateResponseEnvelope(JsonElement root, string requestId)
    {
        if (root.ValueKind != JsonValueKind.Object)
            throw new InvalidDataException("Go daemon returned a non-object IPC response");

        var propertyNames = new HashSet<string>(StringComparer.Ordinal);
        foreach (var property in root.EnumerateObject())
        {
            if (!propertyNames.Add(property.Name))
                throw new InvalidDataException($"Go daemon returned duplicate IPC response field '{property.Name}'");
        }

        if (!root.TryGetProperty("version", out var version) || version.ValueKind != JsonValueKind.Number ||
            !version.TryGetInt32(out var protocolVersion) || protocolVersion != ProtocolVersion ||
            !root.TryGetProperty("id", out var id) || id.ValueKind != JsonValueKind.String || id.GetString() != requestId ||
            !root.TryGetProperty("productVersion", out var productVersion) || productVersion.ValueKind != JsonValueKind.String ||
            string.IsNullOrWhiteSpace(productVersion.GetString()))
            throw new InvalidDataException("Go daemon returned an incompatible or mismatched IPC response");

        var expectedProductVersion = typeof(IPC).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion
            ?.Split('+', 2)[0];
        if (string.IsNullOrWhiteSpace(expectedProductVersion) || productVersion.GetString() != expectedProductVersion)
            throw new InvalidDataException("Local FnCPN components have different product versions; complete installation and restart FnCPN");

        if (!root.TryGetProperty("ok", out var ok) || ok.ValueKind is not (JsonValueKind.True or JsonValueKind.False))
            throw new InvalidDataException("Go daemon returned an invalid IPC success flag");

        var hasError = root.TryGetProperty("error", out var error);
        var hasResult = root.TryGetProperty("result", out _);
        if (ok.GetBoolean())
        {
            if (hasError && error.ValueKind != JsonValueKind.Null)
                throw new InvalidDataException("Successful IPC response contains an error");
            return;
        }

        var errorPropertyNames = new HashSet<string>(StringComparer.Ordinal);
        if (error.ValueKind == JsonValueKind.Object)
        {
            foreach (var property in error.EnumerateObject())
            {
                if (!errorPropertyNames.Add(property.Name))
                    throw new InvalidDataException($"Go daemon returned duplicate IPC error field '{property.Name}'");
            }
        }
        if (hasResult || !hasError || error.ValueKind != JsonValueKind.Object ||
            !error.TryGetProperty("code", out var code) || code.ValueKind != JsonValueKind.String || string.IsNullOrWhiteSpace(code.GetString()) ||
            !error.TryGetProperty("message", out var message) || message.ValueKind != JsonValueKind.String || string.IsNullOrWhiteSpace(message.GetString()))
            throw new InvalidDataException("Go daemon returned an invalid IPC failure response");
        if (!IsKnownErrorCode(code.GetString()!))
            throw new InvalidDataException("Go daemon returned an unknown IPC error code");
    }

    private static bool IsKnownErrorCode(string code) => code switch
    {
        "INVALID_ARGUMENT" or "AUTH_REQUIRED" or "PERMISSION_DENIED" or "NOT_FOUND" or "ALREADY_EXISTS" or
        "FAILED_PRECONDITION" or "DEVICE_REVOKED" or "UNAVAILABLE" or "CONFLICT" or "TIMEOUT" or
        "CANCELED" or "INTERNAL" or "PROTOCOL_ERROR" or "SERVER_UNAVAILABLE" or "VERSION_INCOMPATIBLE" or
        "RESOURCE_EXHAUSTED" or "DISCOVERY_FAILED" => true,
        _ => false
    };

    internal static TimeSpan TimeoutForMethod(string method) => method switch
    {
        "authorize-native" or "watch-client-status" => TimeSpan.FromSeconds(310),
        "connect" or "retry" => TimeSpan.FromSeconds(45),
        _ => TimeSpan.FromSeconds(30)
    };

    public static async Task<T> CallAsync<T>(string method, object? parameters = null, CancellationToken cancellationToken = default)
    {
        var result = await CallAsync(method, parameters, cancellationToken);
        return result.Deserialize<T>(Json) ?? throw new InvalidDataException("Go daemon returned an empty result");
    }

    private static async Task WriteFrameAsync(Stream stream, byte[] payload, CancellationToken token)
    {
        if (payload.Length is 0 or > 262144) throw new InvalidDataException("IPC request frame size is invalid");
        var header = new byte[4];
        BinaryPrimitives.WriteUInt32BigEndian(header, (uint)payload.Length);
        await stream.WriteAsync(header, token);
        await stream.WriteAsync(payload, token);
        await stream.FlushAsync(token);
    }

    private static async Task<JsonDocument> ReadFrameAsync(Stream stream, CancellationToken token)
    {
        var header = new byte[4];
        await stream.ReadExactlyAsync(header, token);
        var size = BinaryPrimitives.ReadUInt32BigEndian(header);
        if (size is 0 or > 262144) throw new InvalidDataException("IPC response frame size is invalid");
        var payload = new byte[size];
        await stream.ReadExactlyAsync(payload, token);
        return JsonDocument.Parse(payload);
    }

    private sealed record Request(int Version, string Id, string Method, [property: JsonPropertyName("params")] object? Parameters);
}

internal sealed class IPCException(string message, JsonElement details) : Exception(message)
{
    public JsonElement Details { get; } = details;
}

internal sealed record AuthorizationResult(string RequestId, string State, RemoteError? Error);
internal sealed record StatusWatchResult(bool Changed, ulong Generation, ClientStatus Status);
internal sealed record RemoteError(
    string Code,
    string Message,
    string? Detail,
    string? Operation,
    string? RemoteCode = null,
    int? HttpStatus = null,
    string? RequestId = null,
    string? UpgradeTarget = null,
    string? ClientVersion = null,
    string? ServerVersion = null)
{
    public string? ReleaseURL { get; init; }
}
internal sealed record ClientStatus(string State, string? Path, string? Interface, int Mtu, DateTimeOffset? LastHandshake, RemoteError? LastError, bool LocalNetworkWaiting, string? LocalNetworkAccess);
internal sealed record Diagnostics(ClientStatus Status, string? FnId, string? Username, string? ClientAddress, string? NasAddress, bool Administrator, bool PrivilegedAvailable, bool PrivilegedActive, bool PrivilegedDegraded, string? NetworkInterface, string? NetworkFingerprint, string? HandshakeAge, int Mtu, bool LanOverlap, string? RoutePolicy, DirectDiagnostics Direct, RemoteError[]? Errors = null);
internal sealed record DirectDiagnostics(bool LocalPublicIPv6, int CandidateCount, int AttemptCount, string? Endpoint, string? Reason, RemoteError? LastError);
