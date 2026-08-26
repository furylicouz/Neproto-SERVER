import 'package:flutter/material.dart' hide DiagnosticLevel;
import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/design/app_theme.dart';
import 'package:neproto_client/screens/diagnostics/diagnostics_screen.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  testWidgets('diagnostics render stable code stage versions and policy', (
    tester,
  ) async {
    var refreshes = 0;
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: DiagnosticsScreen(
          snapshot: snapshot(),
          loading: false,
          onRefresh: () async {
            refreshes++;
          },
        ),
      ),
    );

    expect(find.text('http3-only'), findsOneWidget);
    expect(find.text('HTTP3_TIMEOUT'), findsOneWidget);
    expect(find.text('WEBTRANSPORT_CONNECT'), findsOneWidget);
    expect(find.text('Core 0.1.0'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey<String>('refresh-diagnostics')));
    await tester.pump();
    expect(refreshes, 1);
  });

  testWidgets('diagnostics have bounded useful empty state', (tester) async {
    final empty = snapshot()..events = <DiagnosticEvent>[];
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: DiagnosticsScreen(
          snapshot: empty,
          loading: false,
          onRefresh: () async {},
        ),
      ),
    );

    expect(find.text('Событий пока нет'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

DiagnosticsSnapshot snapshot() {
  return DiagnosticsSnapshot(
    appVersion: '0.1.0',
    hostVersion: '0.1.0',
    coreVersion: '0.1.0',
    carrierPolicy: 'http3-only',
    currentCarrier: CarrierKind.none,
    reconnectCount: 2,
    events: <DiagnosticEvent>[
      DiagnosticEvent(
        unixMs: 1000,
        level: DiagnosticLevel.error,
        stage: ErrorStage.webTransportConnect,
        code: HostErrorCode.http3Timeout,
        message: 'HTTP/3 WebTransport deadline expired.',
        operationId: 'connect-1',
        sequence: 2,
      ),
    ],
  );
}
