import 'dart:async';

import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import 'application/client_session_controller.dart';
import 'design/app_theme.dart';
import 'host/client_host.dart';
import 'screens/home/home_screen.dart';

final class NeprotoApp extends StatefulWidget {
  const NeprotoApp({required this.host, super.key});

  final ClientHost host;

  @override
  State<NeprotoApp> createState() => _NeprotoAppState();
}

final class _NeprotoAppState extends State<NeprotoApp> {
  late final ClientSessionController _controller;

  @override
  void initState() {
    super.initState();
    _controller = ClientSessionController(widget.host);
    unawaited(_controller.start());
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
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
          return HomeScreen(
            state: state,
            onPrimaryAction: () {
              if (state.status.state == TunnelState.connected) {
                return _controller.disconnect();
              }
              final profileId = state.status.profileId;
              if (profileId == null) {
                return Future<void>.value();
              }
              return _controller.connect(profileId);
            },
          );
        },
      ),
    );
  }
}
