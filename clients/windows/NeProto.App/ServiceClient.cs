using System.Buffers.Binary;
using System.IO;
using System.IO.Pipes;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace NeProto.App;

internal interface IServiceClient
{
    Task<T?> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken = default);
}

public sealed class ServiceClient : IServiceClient
{
    private const int MaximumMessageBytes = 256 * 1024;
    private const string PipeName = "NeProto.Service.v1";
    private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(1.5);
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(12);
    private const string ServiceUnavailableMessage = "Служба NeProto не отвечает. Перезапустите приложение или переустановите клиент.";
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
    };

    public async Task<T?> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken = default)
    {
        try
        {
            using var requestTimeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            requestTimeout.CancelAfter(RequestTimeout);
            await using var pipe = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
            using (var connectTimeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
            {
                connectTimeout.CancelAfter(ConnectTimeout);
                await pipe.ConnectAsync(connectTimeout.Token);
            }

            var request = JsonSerializer.SerializeToUtf8Bytes(new
            {
                id = Guid.NewGuid().ToString("N"),
                method,
                @params = parameters
            }, JsonOptions);
            await WriteFrameAsync(pipe, request, requestTimeout.Token);
            var responseBytes = await ReadFrameAsync(pipe, requestTimeout.Token);
            using var document = JsonDocument.Parse(responseBytes);
            var root = document.RootElement;
            if (!root.GetProperty("ok").GetBoolean())
            {
                throw new NeProtoServiceException(root.TryGetProperty("error", out var error) ? error.GetString() ?? "Операция не выполнена" : "Операция не выполнена");
            }
            return root.TryGetProperty("result", out var result)
                ? result.Deserialize<T>(JsonOptions)
                : default;
        }
        catch (Exception error) when (error is OperationCanceledException or IOException or UnauthorizedAccessException)
        {
            throw TranslateTransportError(error, cancellationToken.IsCancellationRequested);
        }
    }

    internal static Exception TranslateTransportError(Exception error, bool callerCancellationRequested) =>
        error is OperationCanceledException && callerCancellationRequested
            ? error
            : new NeProtoServiceUnavailableException(ServiceUnavailableMessage, error);

    private static async Task WriteFrameAsync(Stream stream, byte[] payload, CancellationToken cancellationToken)
    {
        if (payload.Length is 0 or > MaximumMessageBytes) throw new InvalidDataException("Некорректный размер IPC-запроса.");
        var header = new byte[4];
        BinaryPrimitives.WriteInt32LittleEndian(header, payload.Length);
        await stream.WriteAsync(header, cancellationToken);
        await stream.WriteAsync(payload, cancellationToken);
        await stream.FlushAsync(cancellationToken);
    }

    private static async Task<byte[]> ReadFrameAsync(Stream stream, CancellationToken cancellationToken)
    {
        var header = new byte[4];
        await stream.ReadExactlyAsync(header, cancellationToken);
        var size = BinaryPrimitives.ReadInt32LittleEndian(header);
        if (size is <= 0 or > MaximumMessageBytes) throw new InvalidDataException("Некорректный размер IPC-ответа.");
        var payload = new byte[size];
        await stream.ReadExactlyAsync(payload, cancellationToken);
        return payload;
    }
}

public sealed class NeProtoServiceException(string message) : Exception(message);

public sealed class NeProtoServiceUnavailableException(string message, Exception? innerException = null) :
    Exception(message, innerException);
