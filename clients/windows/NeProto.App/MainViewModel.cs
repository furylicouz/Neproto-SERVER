using System.Collections.ObjectModel;
using System.Globalization;
using System.Windows.Media;
using System.Windows.Threading;

namespace NeProto.App;

public sealed class MainViewModel : ObservableObject, IDisposable
{
    private readonly IServiceClient _client;
    private readonly DispatcherTimer _timer;
    private string _section = "home";
    private StatusDto _status = new("stopped", null, null, null, null, null, null, 0, 0, 0, 0, null, 0, 0);
    private string? _serviceError;
    private bool _refreshing;
    private string? _catalogSyncedFor;
    private string? _catalogAttemptedFor;

    internal MainViewModel(IServiceClient client)
    {
        _client = client;
        _timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _timer.Tick += async (_, _) => await RefreshAsync(false);
        _timer.Start();
    }

    public ObservableCollection<ProfileDto> Profiles { get; } = [];
    public ObservableCollection<LogEntryDto> Logs { get; } = [];
    public ObservableCollection<RouteDto> Routes { get; } = [];

    public bool IsHome => _section == "home";
    public bool IsRoutes => _section == "routes";
    public bool IsLogs => _section == "logs";
    public string Header => IsHome ? "Главная" : IsRoutes ? "Маршруты" : "Журнал";
    public string PageSubtitle => IsHome
        ? "Системный VPN-клиент NP/2 Constellation"
        : IsRoutes ? "Правила направления трафика через узлы кластера" : "События приложения и туннельной службы";
    public string ProfileName => _status.ProfileName ?? "Добавьте сервер";
    public string ServerIdentity => _status.ServerIdentity ?? "NP/2 профиль не выбран";
    public string StatusText => _status.State switch
    {
        "connected" => "Подключено",
        "connecting" => "Подключение…",
        "disconnecting" => "Отключение…",
        "failed" => "Ошибка",
        _ => "Не подключено"
    };
    public string DurationText => _status.ConnectedSince is { } since && _status.State == "connected"
        ? (DateTimeOffset.UtcNow - since).ToString(@"hh\:mm\:ss", CultureInfo.InvariantCulture)
        : "00:00:00";
    public string DownloadText => FormatRate(_status.DownloadBytesPerSecond);
    public string UploadText => FormatRate(_status.UploadBytesPerSecond);
    public string DownloadTotalText => FormatBytes(_status.DownloadTotalBytes);
    public string UploadTotalText => FormatBytes(_status.UploadTotalBytes);
    public string CarrierText => string.IsNullOrWhiteSpace(_status.Carrier) ? "NP/2" : $"NP/2 · {_status.Carrier!.ToUpperInvariant()}";
    public string PoolText => _status.CarrierPoolTarget > 0 ? $"Каналы {_status.CarrierPoolHealthy}/{_status.CarrierPoolTarget}" : "Готов к подключению";
    public string LocationGlyph => CountryFlag(SelectedProfile?.Region) ?? "🌐";
    public string SignalText => _status.State == "connected" ? "▂▄▆█" : "▂▂▂▂";
    public string ToggleGlyph => _status.State == "connected" ? "" : "";
    public string ToggleActionText => _status.State switch
    {
        "connected" => "Отключить",
        "connecting" => "Подключение…",
        "disconnecting" => "Отключение…",
        _ => "Подключить"
    };
    public string ConnectionDescription => _status.State switch
    {
        "connected" => "Системный трафик направляется через защищённый NP/2-туннель.",
        "connecting" => "Устанавливаем защищённую сессию с выбранным сервером.",
        "disconnecting" => "Завершаем сессию и восстанавливаем системные маршруты.",
        "failed" => "Последняя попытка подключения завершилась ошибкой.",
        _ => SelectedProfile is null ? "Добавьте сервер NP/2, чтобы начать работу." : "Клиент готов установить защищённое соединение."
    };
    public string RegionText => string.IsNullOrWhiteSpace(SelectedProfile?.Region) ? "Локация не указана" : SelectedProfile!.Region!;
    public bool IsBusy => _status.State is "connecting" or "disconnecting";
    public bool HasServiceError => !string.IsNullOrWhiteSpace(_serviceError);
    public string ServiceError => _serviceError ?? string.Empty;
    public Brush ConnectionBrush => _status.State switch
    {
        "connected" => new SolidColorBrush(Color.FromRgb(34, 197, 94)),
        "connecting" or "disconnecting" => new SolidColorBrush(Color.FromRgb(245, 158, 11)),
        "failed" => new SolidColorBrush(Color.FromRgb(239, 68, 68)),
        _ => new SolidColorBrush(Color.FromRgb(115, 115, 115))
    };
    public Brush ActionBrush => _status.State switch
    {
        "connected" => new SolidColorBrush(Color.FromRgb(34, 197, 94)),
        "connecting" or "disconnecting" => new SolidColorBrush(Color.FromRgb(245, 158, 11)),
        _ => new SolidColorBrush(Color.FromRgb(79, 140, 255))
    };
    public Brush ActionForegroundBrush => new SolidColorBrush(Colors.White);
    public ProfileDto? SelectedProfile => Profiles.FirstOrDefault(profile => profile.Id == _status.SelectedProfileId);
    public bool HasNoRoutes => Routes.Count == 0;

    public void Navigate(string section)
    {
        _section = section;
        Raise(nameof(IsHome)); Raise(nameof(IsRoutes)); Raise(nameof(IsLogs)); Raise(nameof(Header)); Raise(nameof(PageSubtitle));
    }

    public async Task InitializeAsync()
    {
        if (!await LoadProfilesAsync()) return;
        await RefreshAsync(true);
    }

    public async Task ToggleAsync()
    {
        if (IsBusy) return;
        try
        {
            var method = _status.State == "connected" ? "tunnel.disconnect" : "tunnel.connect";
            await _client.CallAsync<object>(method, new { });
            await RefreshAsync(true);
        }
        catch (Exception error) { SetError(error); }
    }

    public async Task<bool> LoadProfilesAsync()
    {
        try
        {
            var payload = await _client.CallAsync<ProfilesPayload>("profiles.list", new { });
            Profiles.Clear();
            foreach (var profile in payload?.Profiles ?? []) Profiles.Add(profile);
            RaiseAll();
            return true;
        }
        catch (Exception error) { SetError(error); return false; }
    }

    public async Task ImportAsync(string uri)
    {
        await _client.CallAsync<ProfileDto>("profiles.import", new { uri });
        await LoadProfilesAsync();
        await RefreshAsync(true);
    }

    public async Task SelectAsync(string id)
    {
        await _client.CallAsync<object>("profiles.select", new { id });
        await LoadProfilesAsync(); await RefreshAsync(true);
    }

    public async Task RemoveAsync(string id)
    {
        await _client.CallAsync<object>("profiles.remove", new { id });
        await LoadProfilesAsync(); await RefreshAsync(true);
    }

    public async Task SyncCatalogAsync()
    {
        var profileId = _status.SelectedProfileId;
        if (string.IsNullOrWhiteSpace(profileId)) return;
        _catalogAttemptedFor = profileId;
        await SyncCatalogCoreAsync(profileId);
    }

    public async Task<bool> LoadRoutesAsync()
    {
        try
        {
            var routes = await _client.CallAsync<RouteDto[]>("routes.list", new { }) ?? [];
            Routes.Clear(); foreach (var route in routes) Routes.Add(route);
            Raise(nameof(HasNoRoutes));
            return true;
        }
        catch (Exception error) { SetError(error); return false; }
    }

    public async Task SaveRouteAsync(string name, string domains, string cidrs, string action, string? nodeId)
    {
        var domainValues = SplitTokens(domains).Select(value => value.ToLowerInvariant()).ToArray();
        var cidrValues = SplitTokens(cidrs).ToArray();
        var route = new
        {
            id = "local-" + Guid.NewGuid().ToString("N"), name, priority = 100, enabled = true,
            source = "client", mandatory = false,
            match = new { domain_suffixes = domainValues, cidrs = cidrValues },
            action = new { kind = action, node_ids = action is "node" or "chain" && !string.IsNullOrWhiteSpace(nodeId) ? new[] { nodeId } : Array.Empty<string>() }
        };
        await _client.CallAsync<object>("routes.upsert", new { route });
        await LoadRoutesAsync();
    }

    public async Task RemoveRouteAsync(string id)
    {
        await _client.CallAsync<object>("routes.remove", new { id });
        await LoadRoutesAsync();
    }

    public async Task<bool> RefreshAsync(bool includeLists)
    {
        if (_refreshing) return true;
        _refreshing = true;
        try
        {
            _status = await _client.CallAsync<StatusDto>("status", new { }) ?? _status;
            _serviceError = _status.LastError;
            if (includeLists) await LoadProfilesAsync();
            if (_status.State != "connected")
            {
                _catalogAttemptedFor = null;
                _catalogSyncedFor = null;
            }
            RaiseAll();
            if (_status.State == "connected" && SelectedProfile?.ClusterId is not null &&
                _catalogSyncedFor != _status.SelectedProfileId && _catalogAttemptedFor != _status.SelectedProfileId)
            {
                await TryAutoSyncCatalogAsync(_status.SelectedProfileId!);
            }
            if (includeLists && _catalogSyncedFor != _status.SelectedProfileId) await LoadRoutesAsync();
            if (IsLogs) await LoadLogsAsync();
            RaiseAll();
            return true;
        }
        catch (Exception error) { SetError(error); return false; }
        finally { _refreshing = false; }
    }

    private async Task TryAutoSyncCatalogAsync(string profileId)
    {
        _catalogAttemptedFor = profileId;
        try { await SyncCatalogCoreAsync(profileId); }
        catch { /* The tunnel stays usable; the service records one warning for diagnostics. */ }
    }

    private async Task SyncCatalogCoreAsync(string profileId)
    {
        await _client.CallAsync<object>("catalog.sync", new { });
        _catalogSyncedFor = profileId;
        await LoadProfilesAsync();
        await LoadRoutesAsync();
    }

    internal async Task<bool> LoadLogsAsync()
    {
        try
        {
            var entries = await _client.CallAsync<LogEntryDto[]>("logs.tail", new { limit = 200 }) ?? [];
            Logs.Clear(); foreach (var entry in entries.Reverse()) Logs.Add(entry);
            return true;
        }
        catch (Exception error) { SetError(error); return false; }
    }

    private void SetError(Exception error) { _serviceError = error.Message; Raise(nameof(HasServiceError)); Raise(nameof(ServiceError)); }
    private void RaiseAll()
    {
        foreach (var name in new[] { nameof(ProfileName), nameof(ServerIdentity), nameof(StatusText), nameof(DurationText), nameof(DownloadText), nameof(UploadText), nameof(DownloadTotalText), nameof(UploadTotalText), nameof(CarrierText), nameof(PoolText), nameof(LocationGlyph), nameof(SignalText), nameof(ToggleGlyph), nameof(ToggleActionText), nameof(ConnectionDescription), nameof(RegionText), nameof(IsBusy), nameof(HasServiceError), nameof(ServiceError), nameof(ConnectionBrush), nameof(ActionBrush), nameof(ActionForegroundBrush), nameof(SelectedProfile) }) Raise(name);
    }

    private static string FormatRate(long bytes) => bytes switch
    {
        >= 1_048_576 => $"{bytes / 1_048_576d:0.0} МБ/с",
        >= 1024 => $"{bytes / 1024d:0} КБ/с",
        _ => $"{bytes} Б/с"
    };

    private static string FormatBytes(long bytes) => bytes switch
    {
        >= 1_073_741_824 => $"{bytes / 1_073_741_824d:0.00} ГБ",
        >= 1_048_576 => $"{bytes / 1_048_576d:0.0} МБ",
        >= 1024 => $"{bytes / 1024d:0} КБ",
        _ => $"{bytes} Б"
    };

    private static IEnumerable<string> SplitTokens(string value) => value.Split([',', ';', ' ', '\r', '\n', '\t'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);

    private static string? CountryFlag(string? region)
    {
        if (string.IsNullOrWhiteSpace(region)) return null;
        var country = region.Trim().ToLowerInvariant();
        return country switch { "russia" or "россия" => "🇷🇺", "netherlands" or "нидерланды" => "🇳🇱", "germany" or "германия" => "🇩🇪", "finland" or "финляндия" => "🇫🇮", "france" or "франция" => "🇫🇷", "usa" or "united states" or "сша" => "🇺🇸", _ => "🌐" };
    }

    public void Dispose() => _timer.Stop();
}
