import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/design/app_theme.dart';
import 'package:neproto_client/screens/profiles/profiles_screen.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  testWidgets('profiles screen renders select remove and import actions', (
    tester,
  ) async {
    var selected = '';
    var removed = '';
    var imported = '';
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: ProfilesScreen(
          profiles: <ProfileSummary>[
            profile('primary'),
            profile('backup', selected: false),
          ],
          busy: false,
          onSelect: (id) async {
            selected = id;
          },
          onRemove: (id) async {
            removed = id;
          },
          onImport: (value) async {
            imported = value;
            return true;
          },
        ),
      ),
    );

    expect(find.text('Primary'), findsOneWidget);
    expect(find.text('Backup'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey<String>('select-backup')));
    await tester.pump();
    expect(selected, 'backup');

    await tester.tap(find.byKey(const ValueKey<String>('remove-primary')));
    await tester.pump();
    expect(removed, 'primary');

    await tester.tap(find.byKey(const ValueKey<String>('import-profile')));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey<String>('onboarding-input')),
      'np2://import/v2/one-time-value',
    );
    await tester.tap(find.text('Импортировать'));
    await tester.pumpAndSettle();
    expect(imported, 'np2://import/v2/one-time-value');
  });

  testWidgets('profiles screen has a useful empty state', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: ProfilesScreen(
          profiles: const <ProfileSummary>[],
          busy: false,
          onSelect: (_) async {},
          onRemove: (_) async {},
          onImport: (_) async => false,
        ),
      ),
    );

    expect(find.text('Нет профилей'), findsOneWidget);
    expect(find.text('Добавить профиль'), findsOneWidget);
  });

  testWidgets('profiles screen is overflow-free at 320 logical pixels', (
    tester,
  ) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(320, 900);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark(),
        home: ProfilesScreen(
          profiles: <ProfileSummary>[
            profile('primary'),
            profile('backup', selected: false),
          ],
          busy: false,
          onSelect: (_) async {},
          onRemove: (_) async {},
          onImport: (_) async => true,
        ),
      ),
    );

    expect(tester.takeException(), isNull);
  });
}

ProfileSummary profile(String id, {bool selected = true}) {
  return ProfileSummary(
    id: id,
    displayName: id == 'primary' ? 'Primary' : 'Backup',
    serverIdentity: 'vpn.example.test',
    host: 'vpn.example.test',
    selected: selected,
    hasCredential: true,
    origin: ProfileOrigin.imported,
    catalogManaged: false,
    updatedAtUnixMs: 1,
  );
}
