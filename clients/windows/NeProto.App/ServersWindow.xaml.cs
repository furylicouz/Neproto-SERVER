using System.Windows;

namespace NeProto.App;

public partial class ServersWindow : Window
{
    private readonly MainViewModel _viewModel;
    public ServersWindow(MainViewModel viewModel) { _viewModel = viewModel; WindowAppearance.UseDarkTitleBar(this); InitializeComponent(); DataContext = viewModel; ProfilesList.SelectedItem = viewModel.SelectedProfile; }

    private async void Select_Click(object sender, RoutedEventArgs e)
    {
        if (ProfilesList.SelectedItem is not ProfileDto profile) return;
        try { await _viewModel.SelectAsync(profile.Id); DialogResult = true; Close(); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось выбрать сервер", MessageBoxButton.OK, MessageBoxImage.Error); }
    }

    private async void Remove_Click(object sender, RoutedEventArgs e)
    {
        if (ProfilesList.SelectedItem is not ProfileDto profile) return;
        if (MessageBox.Show(this, $"Удалить сервер «{profile.Name}»?", "NeProto", MessageBoxButton.YesNo, MessageBoxImage.Question) != MessageBoxResult.Yes) return;
        try { await _viewModel.RemoveAsync(profile.Id); }
        catch (Exception error) { MessageBox.Show(this, error.Message, "Не удалось удалить сервер", MessageBoxButton.OK, MessageBoxImage.Error); }
    }

    private void Close_Click(object sender, RoutedEventArgs e) => Close();
}
