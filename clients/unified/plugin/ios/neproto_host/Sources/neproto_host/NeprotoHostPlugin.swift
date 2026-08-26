import Flutter

public class NeprotoHostPlugin: NSObject, FlutterPlugin {
  public static func register(with registrar: FlutterPluginRegistrar) {
    MainActor.assumeIsolated {
      let host = IOSClientHost(binaryMessenger: registrar.messenger())
      ClientHostApiSetup.setUp(binaryMessenger: registrar.messenger(), api: host)
    }
  }
}
