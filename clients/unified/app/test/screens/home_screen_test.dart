import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/application/client_session_controller.dart';
import 'package:neproto_client/design/app_theme.dart';
import 'package:neproto_client/screens/home/home_screen.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  final cases = <TunnelState, String>{
    TunnelState.disconnected: 'Отключено',
    TunnelState.connecting: 'Подключение',
    TunnelState.connected: 'Подключено',
    TunnelState.reconnecting: 'Переподключение',
    TunnelState.disconnecting: 'Отключение',
    TunnelState.failed: 'Ошибка',
  };

  for (final entry in cases.entries) {
    testWidgets('renders ${entry.key.name} home state', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.dark(),
          home: HomeScreen(
            state: stateFor(entry.key),
            onPrimaryAction: () async {},
          ),
        ),
      );

      expect(find.text(entry.value), findsNWidgets(2));
      expect(find.text('HTTP/3 WebTransport'), findsOneWidget);
      expect(find.byType(DropdownButton<Object>), findsNothing);
    });
  }

  testWidgets('primary tunnel action is accessible and guarded while busy', (
    tester,
  ) async {
    var calls = 0;
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: HomeScreen(
          state: stateFor(TunnelState.disconnected),
          onPrimaryAction: () async {
            calls++;
          },
        ),
      ),
    );

    final action = find.byKey(const ValueKey<String>('primary-tunnel-action'));
    expect(action, findsOneWidget);
    expect(
      tester.getSemantics(action),
      matchesSemantics(
        label: 'Подключить VPN через HTTP/3 WebTransport',
        isButton: true,
        hasEnabledState: true,
        isEnabled: true,
        hasTapAction: true,
      ),
    );
    await tester.tap(action);
    await tester.pump();
    expect(calls, 1);

    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: HomeScreen(
          state: stateFor(TunnelState.reconnecting, commandPending: true),
          onPrimaryAction: () async {
            calls++;
          },
        ),
      ),
    );
    await tester.tap(action);
    await tester.pump();
    expect(calls, 1);
  });

  for (final width in <double>[320, 768, 1440]) {
    testWidgets('home is overflow-free at ${width.toInt()} logical pixels', (
      tester,
    ) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = Size(width, 900);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.view.resetPhysicalSize);

      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.dark(),
          home: HomeScreen(
            state: stateFor(TunnelState.reconnecting),
            onPrimaryAction: () async {},
          ),
        ),
      );

      expect(tester.takeException(), isNull);
    });
  }
}

ClientSessionState stateFor(TunnelState state, {bool commandPending = false}) {
  return ClientSessionState(
    loading: false,
    ready: true,
    commandPending: commandPending,
    status: TunnelStatus(
      state: state,
      profileId: 'profile-1',
      carrier: state == TunnelState.connected
          ? CarrierKind.http3WebTransport
          : CarrierKind.none,
      connectedAtUnixMs: state == TunnelState.connected ? 1000 : 0,
      uploadBytesPerSecond: 128000,
      downloadBytesPerSecond: 512000,
      uploadTotalBytes: 1024000,
      downloadTotalBytes: 4096000,
      sequence: 1,
      lastError: state == TunnelState.failed
          ? HostError(
              code: HostErrorCode.http3Timeout,
              stage: ErrorStage.webTransportConnect,
              message: 'HTTP/3 WebTransport deadline expired.',
              retryable: true,
              operationId: 'connect-1',
            )
          : null,
    ),
  );
}
