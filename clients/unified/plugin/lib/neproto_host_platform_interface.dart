import 'package:plugin_platform_interface/plugin_platform_interface.dart';

import 'neproto_host_method_channel.dart';

abstract class NeprotoHostPlatform extends PlatformInterface {
  /// Constructs a NeprotoHostPlatform.
  NeprotoHostPlatform() : super(token: _token);

  static final Object _token = Object();

  static NeprotoHostPlatform _instance = MethodChannelNeprotoHost();

  /// The default instance of [NeprotoHostPlatform] to use.
  ///
  /// Defaults to [MethodChannelNeprotoHost].
  static NeprotoHostPlatform get instance => _instance;

  /// Platform-specific implementations should set this with their own
  /// platform-specific class that extends [NeprotoHostPlatform] when
  /// they register themselves.
  static set instance(NeprotoHostPlatform instance) {
    PlatformInterface.verifyToken(instance, _token);
    _instance = instance;
  }

  Future<String?> getPlatformVersion() {
    throw UnimplementedError('platformVersion() has not been implemented.');
  }
}
