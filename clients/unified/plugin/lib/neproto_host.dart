
import 'neproto_host_platform_interface.dart';

class NeprotoHost {
  Future<String?> getPlatformVersion() {
    return NeprotoHostPlatform.instance.getPlatformVersion();
  }
}
