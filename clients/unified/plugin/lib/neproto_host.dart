
export 'src/generated/client_host_api.g.dart';
export 'src/host_api_version.dart';

import 'neproto_host_platform_interface.dart';

class NeprotoHost {
  Future<String?> getPlatformVersion() {
    return NeprotoHostPlatform.instance.getPlatformVersion();
  }
}
