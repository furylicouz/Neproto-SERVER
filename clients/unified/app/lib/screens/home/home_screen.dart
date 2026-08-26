import 'package:flutter/material.dart';

import '../../application/client_session_controller.dart';
import '../../design/app_theme.dart';
import 'home_presenter.dart';
import 'tunnel_card.dart';

final class HomeScreen extends StatelessWidget {
  const HomeScreen({
    required this.state,
    required this.onPrimaryAction,
    super.key,
  });

  final ClientSessionState state;
  final Future<void> Function() onPrimaryAction;

  @override
  Widget build(BuildContext context) {
    final presentation = presentTunnelState(state.status.state);
    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 720),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: <Widget>[
                  _Header(presentation: presentation),
                  const SizedBox(height: 24),
                  TunnelCard(
                    state: state,
                    presentation: presentation,
                    onPrimaryAction: onPrimaryAction,
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

final class _Header extends StatelessWidget {
  const _Header({required this.presentation});

  final HomeStatusPresentation presentation;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 400;
        return Row(
          children: <Widget>[
            Container(
              width: 40,
              height: 40,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: AppColors.accent,
                borderRadius: BorderRadius.circular(12),
              ),
              child: const Text(
                'N',
                style: TextStyle(fontSize: 18, fontWeight: FontWeight.w800),
              ),
            ),
            const SizedBox(width: 12),
            const Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: <Widget>[
                  Text(
                    'NeProto',
                    style: TextStyle(fontWeight: FontWeight.w700),
                  ),
                  Text(
                    'NP/2 · HTTP/3',
                    style: TextStyle(
                      color: AppColors.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                ],
              ),
            ),
            Semantics(
              label: 'Состояние: ${presentation.label}',
              child: ExcludeSemantics(
                child: Icon(
                  presentation.icon,
                  color: presentation.color,
                  size: 16,
                ),
              ),
            ),
            if (!compact) ...<Widget>[
              const SizedBox(width: 8),
              Text(presentation.label),
            ],
          ],
        );
      },
    );
  }
}
