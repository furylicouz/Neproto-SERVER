using System.Windows;

namespace NeProto.App;

public partial class ImportWindow : Window
{
    public ImportWindow() { WindowAppearance.UseDarkTitleBar(this); InitializeComponent(); Loaded += (_, _) => UriBox.Focus(); }
    public string ImportUri => UriBox.Text.Trim();
    private void Cancel_Click(object sender, RoutedEventArgs e) { DialogResult = false; Close(); }
    private void Add_Click(object sender, RoutedEventArgs e)
    {
        if (!ImportUri.StartsWith("np2://import/", StringComparison.Ordinal))
        {
            MessageBox.Show(this, "Вставьте полную ссылку импорта NP/2.", "Некорректная конфигурация", MessageBoxButton.OK, MessageBoxImage.Warning);
            return;
        }
        DialogResult = true; Close();
    }
}
