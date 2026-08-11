using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;

namespace NeProto.App;

internal static class WindowAppearance
{
    private const int ImmersiveDarkMode = 20;
    private const int ImmersiveDarkModeLegacy = 19;

    public static void UseDarkTitleBar(Window window)
    {
        window.SourceInitialized += (_, _) =>
        {
            var handle = new WindowInteropHelper(window).Handle;
            var enabled = 1;
            if (DwmSetWindowAttribute(handle, ImmersiveDarkMode, ref enabled, sizeof(int)) != 0)
            {
                _ = DwmSetWindowAttribute(handle, ImmersiveDarkModeLegacy, ref enabled, sizeof(int));
            }
        };
    }

    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(nint window, int attribute, ref int value, int valueSize);
}
