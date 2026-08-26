import 'dart:async';

import 'package:flutter/material.dart';

import 'application/client_session_controller.dart';
import 'host/client_host.dart';

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
      home: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          final state = _controller.state;
          return Scaffold(
            appBar: AppBar(title: const Text('NeProto')),
            body: Center(
              child: state.loading
                  ? const CircularProgressIndicator()
                  : Text(state.error?.code.name ?? state.status.state.name),
            ),
          );
        },
      ),
    );
  }
}
