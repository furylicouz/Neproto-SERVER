import 'package:pigeon/pigeon.dart';

@ConfigurePigeon(
  PigeonOptions(
    dartOut: 'lib/src/generated/client_host_api.g.dart',
    dartOptions: DartOptions(),
    swiftOut: 'ios/neproto_host/Sources/neproto_host/ClientHostApi.g.swift',
    swiftOptions: SwiftOptions(),
    cppHeaderOut: 'windows/include/neproto_host/client_host_api.g.h',
    cppSourceOut: 'windows/client_host_api.g.cpp',
    cppOptions: CppOptions(namespace: 'neproto_host'),
    dartPackageName: 'neproto_host',
  ),
)
enum HostPlatform { unknown, windows, ios }

class HostApiVersion {
  HostApiVersion({required this.major, required this.minor});

  int major;
  int minor;
}

class HostCapabilities {
  HostCapabilities({
    required this.apiVersion,
    required this.platform,
    required this.appVersion,
    required this.hostVersion,
    required this.coreVersion,
    required this.supportsHttp3WebTransport,
  });

  HostApiVersion apiVersion;
  HostPlatform platform;
  String appVersion;
  String hostVersion;
  String coreVersion;
  bool supportsHttp3WebTransport;
}

@HostApi()
abstract class ClientHostApi {
  @async
  HostCapabilities getCapabilities(HostApiVersion requestedVersion);
}
