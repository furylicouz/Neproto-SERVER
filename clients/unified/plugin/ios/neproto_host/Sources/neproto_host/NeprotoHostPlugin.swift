import Flutter

public class NeprotoHostPlugin: NSObject, FlutterPlugin {
  public static func register(with registrar: FlutterPluginRegistrar) {
    Task { @MainActor in
      let host = IOSClientHost(binaryMessenger: registrar.messenger())
      ClientHostApiSetup.setUp(binaryMessenger: registrar.messenger(), api: host)
    }
  }
}
