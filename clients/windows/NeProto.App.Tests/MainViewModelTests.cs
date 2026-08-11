using Xunit;

namespace NeProto.App.Tests;

public sealed class MainViewModelTests
{
    [Fact]
    public async Task InitializeKeepsUiAliveWhenServiceIsUnavailable()
    {
        using var model = new MainViewModel(new UnavailableServiceClient());

        await model.InitializeAsync();

        Assert.True(model.HasServiceError);
        Assert.Contains("Служба NeProto не отвечает", model.ServiceError);
        Assert.Empty(model.Profiles);
        Assert.Empty(model.Routes);
        Assert.Empty(model.Logs);
    }

    [Fact]
    public async Task ProfileLoadReportsFailureWithoutThrowing()
    {
        using var model = new MainViewModel(new UnavailableServiceClient());

        var loaded = await model.LoadProfilesAsync();

        Assert.False(loaded);
        Assert.True(model.HasServiceError);
    }

    private sealed class UnavailableServiceClient : IServiceClient
    {
        public Task<T?> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken = default) =>
            Task.FromException<T?>(new NeProtoServiceUnavailableException("Служба NeProto не отвечает. Перезапустите приложение или переустановите клиент."));
    }
}
