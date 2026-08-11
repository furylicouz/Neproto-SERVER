using Xunit;

namespace NeProto.App.Tests;

public sealed class ServiceClientTests
{
    [Fact]
    public void TimeoutIsReportedAsServiceUnavailable()
    {
        var error = ServiceClient.TranslateTransportError(new OperationCanceledException(), callerCancellationRequested: false);

        var unavailable = Assert.IsType<NeProtoServiceUnavailableException>(error);
        Assert.Equal("Служба NeProto не отвечает. Перезапустите приложение или переустановите клиент.", unavailable.Message);
    }

    [Fact]
    public void CallerCancellationIsPreserved()
    {
        var source = new OperationCanceledException();

        Assert.Same(source, ServiceClient.TranslateTransportError(source, callerCancellationRequested: true));
    }
}
