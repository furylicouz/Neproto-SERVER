#if os(iOS)
import Flutter
#elseif os(macOS)
import FlutterMacOS
#endif

public class NeprotoHostPlugin: NSObject, FlutterPlugin {
  public static func register(with registrar: FlutterPluginRegistrar) {
    MainActor.assumeIsolated {
#if os(iOS)
	  let messenger = registrar.messenger()
#else
	  let messenger = registrar.messenger
#endif
	  let host = IOSClientHost(binaryMessenger: messenger)
	  ClientHostApiSetup.setUp(binaryMessenger: messenger, api: host)
    }
  }
}
