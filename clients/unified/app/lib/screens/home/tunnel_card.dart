import 'dart:async';

import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import '../../application/client_session_controller.dart';
import '../../design/app_theme.dart';
import 'home_presenter.dart';
import 'traffic_row.dart';

final class TunnelCard extends StatelessWidget {
  const TunnelCard({
    required this.state,
    required this.presentation,
    required this.onPrimaryAction,
    super.key,
  });

  final ClientSessionState state;
  final HomeStatusPresentation presentation;
  final Future<void> Function() onPrimaryAction;

  @override
  Widget build(BuildContext context) {
    final selectedProfileId =
        state.selectedProfile?.id ?? state.status.profileId;
    final canConnect =
        selectedProfileId != null &&
        (state.status.state == TunnelState.disconnected ||
            state.status.state == TunnelState.failed);
    final canDisconnect = state.status.state == TunnelState.connected;
    final enabled =
        state.ready && !state.commandPending && (canConnect || canDisconnect);
    final disconnecting = state.status.state == TunnelState.connected;
    final semanticsLabel = disconnecting
        ? 'Отключить VPN'
        : 'Подключить VPN через HTTP/3 WebTransport';

    return DecoratedBox(
      decoration: BoxDecoration(
        color: AppColors.surface,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(24),
      ),
      child: Column(
        children: <Widget>[
          Padding(
            padding: const EdgeInsets.all(20),
            child: Row(
              children: <Widget>[
                const Icon(Icons.public, color: AppColors.accent),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: <Widget>[
                      Text(
                        state.selectedProfile?.displayName ??
                            state.status.profileId ??
                            'Профиль не выбран',
                        style: Theme.of(context).textTheme.titleLarge,
                      ),
                      const Text(
                        'HTTP/3 WebTransport',
                        style: TextStyle(color: AppColors.textSecondary),
                      ),
                    ],
                  ),
                ),
                const _CarrierBadge(),
              ],
            ),
          ),
          const Divider(height: 1),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 32, 24, 28),
            child: Column(
              children: <Widget>[
                Text(
                  presentation.label,
                  style: Theme.of(context).textTheme.headlineMedium,
                ),
                const SizedBox(height: 8),
                Text(presentation.detail, textAlign: TextAlign.center),
                const SizedBox(height: 28),
                Semantics(
                  key: const ValueKey<String>('primary-tunnel-action'),
                  label: semanticsLabel,
                  button: true,
                  enabled: enabled,
                  onTap: enabled ? () => unawaited(onPrimaryAction()) : null,
                  child: ExcludeSemantics(
                    child: FilledButton(
                      onPressed: enabled
                          ? () => unawaited(onPrimaryAction())
                          : null,
                      style: FilledButton.styleFrom(
                        fixedSize: const Size.square(112),
                        shape: const CircleBorder(),
                        backgroundColor: disconnecting
                            ? AppColors.danger
                            : AppColors.accent,
                      ),
                      child: state.commandPending
                          ? const SizedBox.square(
                              dimension: 28,
                              child: CircularProgressIndicator(strokeWidth: 3),
                            )
                          : Icon(
                              disconnecting
                                  ? Icons.stop_rounded
                                  : Icons.power_settings_new,
                              size: 42,
                            ),
                    ),
                  ),
                ),
                if (state.error != null) ...<Widget>[
                  const SizedBox(height: 24),
                  _ErrorBanner(error: state.error!),
                ],
              ],
            ),
          ),
          const Divider(height: 1),
          TrafficRow(status: state.status),
        ],
      ),
    );
  }
}

final class _CarrierBadge extends StatelessWidget {
  const _CarrierBadge();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.accentMuted,
        borderRadius: BorderRadius.circular(8),
      ),
      child: const Text('H3', style: TextStyle(color: AppColors.accent)),
    );
  }
}

final class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.error});

  final HostError error;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      liveRegion: true,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.danger.withValues(alpha: 0.12),
          border: Border.all(color: AppColors.danger.withValues(alpha: 0.5)),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Text(error.message, textAlign: TextAlign.center),
      ),
    );
  }
}
