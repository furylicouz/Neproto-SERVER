import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/application/client_session_controller.dart';
import 'package:neproto_client/host/fake_client_host.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  test('startup loads existing native profiles and selection', () async {
    final host = FakeClientHost(profiles: <ProfileSummary>[profile('primary')]);
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);

    await controller.start();

    expect(host.callOrder, <String>[
      'getCapabilities',
      'getStatus',
      'listProfiles',
    ]);
    expect(controller.state.profiles, hasLength(1));
    expect(controller.state.selectedProfile?.id, 'primary');
  });

  test(
    'invalid onboarding fails before host and valid value is not retained',
    () async {
      String? observed;
      final host = FakeClientHost(
        onImport: (request) {
          observed = request.onboardingValue;
        },
      );
      final controller = ClientSessionController(host);
      addTearDown(controller.dispose);
      await controller.start();

      expect(
        await controller.importProfile('not-an-onboarding-value'),
        isFalse,
      );
      expect(host.importCalls, 0);
      expect(controller.state.error?.code, HostErrorCode.invalidProfile);

      const onboarding = 'np2://import/v2/one-time-secret-value';
      expect(await controller.importProfile(onboarding), isTrue);
      expect(host.importCalls, 1);
      expect(observed, onboarding);
      expect(
        controller.state.profiles.any(
          (item) => item.toString().contains(onboarding),
        ),
        isFalse,
      );
    },
  );

  test('select and remove update immutable profile state', () async {
    final host = FakeClientHost(
      profiles: <ProfileSummary>[
        profile('primary'),
        profile('backup', selected: false),
      ],
    );
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);
    await controller.start();

    await controller.selectProfile('backup');
    expect(controller.state.selectedProfile?.id, 'backup');
    expect(host.selectRequests, hasLength(1));

    await controller.removeProfile('primary');
    expect(controller.state.profiles.map((item) => item.id), <String>[
      'backup',
    ]);
    expect(host.removeRequests, hasLength(1));
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
