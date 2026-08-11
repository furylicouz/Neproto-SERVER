using System.Windows;
using System.Windows.Threading;

namespace NeProto.App;

public partial class MainWindow : Window
{
    private readonly MainViewModel _viewModel = new(new ServiceClient());

    public MainWindow(bool smokeTest = false, bool serviceSmokeTest = false)
    {
        WindowAppearance.UseDarkTitleBar(this);
        InitializeComponent();
        DataContext = _viewModel;
        if (serviceSmokeTest)
        {
            Loaded += async (_, _) => await RunServiceSmokeTestAsync();
        }
        else if (smokeTest)
        {
            Loaded += (_, _) => Dispatcher.BeginInvoke(() =>
            {
                Close();
                Application.Current.Shutdown(0);
            }, DispatcherPriority.ApplicationIdle);
        }
        else
        {
            Loaded += async (_, _) => await _viewModel.InitializeAsync();
        }
        Closed += (_, _) => _viewModel.Dispose();
    }

    private async Task RunServiceSmokeTestAsync()
    {
        var success = await _viewModel.LoadProfilesAsync();
        success = await _viewModel.RefreshAsync(false) && success;
        success = await _viewModel.LoadRoutesAsync() && success;
        success = await _viewModel.LoadLogsAsync() && success;
        Application.Current.Shutdown(success ? 0 : 1);
    }

    private void Home_Click(object sender, RoutedEventArgs e) => _viewModel.Navigate("home");
    private async void Routes_Click(object sender, RoutedEventArgs e) { _viewModel.Navigate("routes"); _ = await _viewModel.LoadRoutesAsync(); }
    private void Logs_Click(object sender, RoutedEventArgs e) => _viewModel.Navigate("logs");
    private async void Toggle_Click(object sender, RoutedEventArgs e) => await _viewModel.ToggleAsync();

    private async void Add_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new ImportWindow { Owner = this };
        if (dialog.ShowDialog() != true) return;
        try { await _viewModel.ImportAsync(dialog.ImportUri); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось добавить сервер", MessageBoxButton.OK, MessageBoxImage.Error); }
    }

    private async void Servers_Click(object sender, RoutedEventArgs e)
    {
        if (!await _viewModel.LoadProfilesAsync()) return;
        var dialog = new ServersWindow(_viewModel) { Owner = this };
        dialog.ShowDialog();
        await _viewModel.RefreshAsync(true);
    }

    private async void SyncRoutes_Click(object sender, RoutedEventArgs e)
    {
        try { await _viewModel.SyncCatalogAsync(); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось синхронизировать маршруты", MessageBoxButton.OK, MessageBoxImage.Error); }
    }

    private async void AddRoute_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new RouteEditorWindow(_viewModel.Profiles.Where(profile => profile.ClusterNodeId is not null).ToArray()) { Owner = this };
        if (dialog.ShowDialog() != true) return;
        try { await _viewModel.SaveRouteAsync(dialog.RouteName, dialog.Domains, dialog.Cidrs, dialog.Action, dialog.NodeId); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось сохранить маршрут", MessageBoxButton.OK, MessageBoxImage.Error); }
    }

    private async void RemoveRoute_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not FrameworkElement { Tag: string id }) return;
        try { await _viewModel.RemoveRouteAsync(id); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось удалить маршрут", MessageBoxButton.OK, MessageBoxImage.Error); }
    }
}
