using System.IO;
using System.Windows;

namespace NeProto.App;

public partial class App : Application
{
    private int _showingCrashDialog;

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        DispatcherUnhandledException += (_, args) =>
        {
            WriteCrashLog(args.Exception);
            args.Handled = true;
            if (args.Exception is OperationCanceledException or NeProtoServiceUnavailableException) return;
            if (Interlocked.Exchange(ref _showingCrashDialog, 1) != 0) return;
            try
            {
                MessageBox.Show("Произошла внутренняя ошибка. Подробности сохранены в журнале NeProto.", "NeProto", MessageBoxButton.OK, MessageBoxImage.Error);
            }
            finally { Interlocked.Exchange(ref _showingCrashDialog, 0); }
        };

        var smokeTest = e.Args.Any(argument => string.Equals(argument, "--smoke-test", StringComparison.Ordinal));
        var serviceSmokeTest = e.Args.Any(argument => string.Equals(argument, "--service-smoke-test", StringComparison.Ordinal));
        var window = new MainWindow(smokeTest, serviceSmokeTest);
        MainWindow = window;
        window.Show();
    }

    private static void WriteCrashLog(Exception exception)
    {
        try
        {
            var directory = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "NeProto");
            Directory.CreateDirectory(directory);
            var path = Path.Combine(directory, "client-crash.log");
            if (File.Exists(path) && new FileInfo(path).Length > 1024 * 1024)
            {
                File.Delete(path);
            }
            File.AppendAllText(path, $"[{DateTimeOffset.Now:O}] {exception}\n");
        }
        catch
        {
            // Crash reporting must never mask the original application error.
        }
    }
}
