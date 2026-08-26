import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_host/neproto_host.dart';
import 'package:neproto_host/neproto_host_platform_interface.dart';
import 'package:neproto_host/neproto_host_method_channel.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';

class MockNeprotoHostPlatform
    with MockPlatformInterfaceMixin
    implements NeprotoHostPlatform {
  @override
  Future<String?> getPlatformVersion() => Future.value('42');
}

void main() {
  final NeprotoHostPlatform initialPlatform = NeprotoHostPlatform.instance;

  test('$MethodChannelNeprotoHost is the default instance', () {
    expect(initialPlatform, isInstanceOf<MethodChannelNeprotoHost>());
  });

  test('getPlatformVersion', () async {
    NeprotoHost neprotoHostPlugin = NeprotoHost();
    MockNeprotoHostPlatform fakePlatform = MockNeprotoHostPlatform();
    NeprotoHostPlatform.instance = fakePlatform;

    expect(await neprotoHostPlugin.getPlatformVersion(), '42');
  });
}
