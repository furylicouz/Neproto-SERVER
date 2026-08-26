import 'dart:async';

import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import 'application/client_session_controller.dart';
import 'design/app_theme.dart';
import 'host/client_host.dart';
import 'screens/diagnostics/diagnostics_screen.dart';
import 'screens/home/home_screen.dart';
import 'screens/profiles/profiles_screen.dart';

final class NeprotoApp extends StatefulWidget {
  const NeprotoApp({required this.host, super.key});

  final ClientHost host;

  @override
  State<NeprotoApp> createState() => _NeprotoAppState();
}

final class _NeprotoAppState extends State<NeprotoApp>
    with WidgetsBindingObserver {
  late final ClientSessionController _controller;
  int _destination = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _controller = ClientSessionController(widget.host);
    unawaited(_controller.start());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _controller.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      unawaited(_controller.refreshFromHost());
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      debugShowCheckedModeBanner: false,
      title: 'NeProto',
      theme: AppTheme.dark(),
      home: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          final state = _controller.state;
          return Scaffold(
            body: IndexedStack(
              index: _destination,
              children: <Widget>[
                HomeScreen(
                  state: state,
                  onPrimaryAction: () {
                    if (state.status.state == TunnelState.connected) {
                      return _controller.disconnect();
                    }
                    final profileId =
                        state.selectedProfile?.id ?? state.status.profileId;
                    if (profileId == null) {
                      return Future<void>.value();
                    }
                    return _controller.connect(profileId);
                  },
                ),
                ProfilesScreen(
                  profiles: state.profiles,
                  busy: state.commandPending,
                  onSelect: _controller.selectProfile,
                  onRemove: _controller.removeProfile,
                  onImport: (value) async {
                    final accepted = await _controller.importProfile(value);
                    return accepted
                        ? null
                        : _profileImportErrorMessage(_controller.state.error);
                  },
                ),
                DiagnosticsScreen(
                  snapshot: state.diagnostics,
                  loading: state.diagnosticsLoading,
                  onRefresh: _controller.refreshDiagnostics,
                ),
              ],
            ),
            bottomNavigationBar: NavigationBar(
              selectedIndex: _destination,
              onDestinationSelected: (value) {
                setState(() {
                  _destination = value;
                });
                if (value == 2 && _controller.state.diagnostics == null) {
                  unawaited(_controller.refreshDiagnostics());
                }
              },
              destinations: const <NavigationDestination>[
                NavigationDestination(
                  icon: Icon(Icons.home_outlined),
                  selectedIcon: Icon(Icons.home),
                  label: 'Главная',
                ),
                NavigationDestination(
                  icon: Icon(Icons.dns_outlined),
                  selectedIcon: Icon(Icons.dns),
                  label: 'Профили',
                ),
                NavigationDestination(
                  icon: Icon(Icons.monitor_heart_outlined),
                  selectedIcon: Icon(Icons.monitor_heart),
                  label: 'Диагностика',
                ),
              ],
            ),
          );
        },
      ),
    );
  }
}

String _profileImportErrorMessage(HostError? error) {
  return switch (error?.code) {
    HostErrorCode.invalidProfile =>
      'Строка отклонена native-парсером (INVALID_PROFILE)',
    HostErrorCode.credentialUnavailable =>
      'iOS Keychain не сохранил ключ профиля (CREDENTIAL_UNAVAILABLE)',
    HostErrorCode.internalFailure =>
      'Native-хранилище профилей отклонило импорт (INTERNAL)',
    _ => 'Системный модуль iOS недоступен (HOST_UNAVAILABLE)',
  };
}
