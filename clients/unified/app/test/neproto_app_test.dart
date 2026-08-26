import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/host/fake_client_host.dart';
import 'package:neproto_client/neproto_app.dart';

void main() {
  testWidgets('app startup negotiates and renders authoritative state', (
    tester,
  ) async {
    final host = FakeClientHost();

    await tester.pumpWidget(NeprotoApp(host: host));
    await tester.pumpAndSettle();

    expect(host.callOrder, <String>['getCapabilities', 'getStatus']);
    expect(find.text('NeProto'), findsOneWidget);
    expect(find.text('disconnected'), findsOneWidget);
  });
}
