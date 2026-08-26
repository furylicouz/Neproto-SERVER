import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'neproto_host_platform_interface.dart';

/// An implementation of [NeprotoHostPlatform] that uses method channels.
class MethodChannelNeprotoHost extends NeprotoHostPlatform {
  /// The method channel used to interact with the native platform.
  @visibleForTesting
  final methodChannel = const MethodChannel('neproto_host');

  @override
  Future<String?> getPlatformVersion() async {
    final version = await methodChannel.invokeMethod<String>(
      'getPlatformVersion',
    );
    return version;
  }
}
