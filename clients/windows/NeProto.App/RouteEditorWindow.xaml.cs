using System.Windows;
using System.Windows.Controls;

namespace NeProto.App;

public partial class RouteEditorWindow : Window
{
    public RouteEditorWindow(ProfileDto[] servers) { WindowAppearance.UseDarkTitleBar(this); InitializeComponent(); NodeBox.ItemsSource = servers; NodeBox.SelectedIndex = servers.Length > 0 ? 0 : -1; }
    public string RouteName => NameBox.Text.Trim();
    public string Domains => DomainsBox.Text;
    public string Cidrs => CidrsBox.Text;
    public string Action => (ActionBox.SelectedItem as ComboBoxItem)?.Tag?.ToString() ?? "auto";
    public string? NodeId => NodeBox.SelectedValue?.ToString();
    private void Action_Changed(object sender, SelectionChangedEventArgs e) { if (NodeBox is not null) NodeBox.Visibility = Action == "node" ? Visibility.Visible : Visibility.Collapsed; }
    private void Cancel_Click(object sender, RoutedEventArgs e) { DialogResult = false; Close(); }
    private void Save_Click(object sender, RoutedEventArgs e)
    {
        if (string.IsNullOrWhiteSpace(RouteName) || (string.IsNullOrWhiteSpace(Domains) && string.IsNullOrWhiteSpace(Cidrs))) { MessageBox.Show(this, "Укажите название и хотя бы один домен или CIDR.", "Маршрут", MessageBoxButton.OK, MessageBoxImage.Warning); return; }
        if (Action == "node" && string.IsNullOrWhiteSpace(NodeId)) { MessageBox.Show(this, "Выберите сервер кластера.", "Маршрут", MessageBoxButton.OK, MessageBoxImage.Warning); return; }
        DialogResult = true; Close();
    }
}
