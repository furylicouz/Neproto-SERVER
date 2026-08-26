import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart' as host;

import '../../design/app_theme.dart';

final class DiagnosticEventTile extends StatelessWidget {
  const DiagnosticEventTile({required this.event, super.key});

  final host.DiagnosticEvent event;

  @override
  Widget build(BuildContext context) {
    final color = switch (event.level) {
      host.DiagnosticLevel.error => AppColors.danger,
      host.DiagnosticLevel.warning => AppColors.warning,
      host.DiagnosticLevel.info => AppColors.accent,
      host.DiagnosticLevel.unknown => AppColors.textSecondary,
    };
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.surface,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(14),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Wrap(
            spacing: 8,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: <Widget>[
              _Badge(label: enumLabel(event.level.name), color: color),
              if (event.code != null)
                _Badge(label: enumLabel(event.code!.name), color: color),
              _Badge(
                label: enumLabel(event.stage.name),
                color: AppColors.textSecondary,
              ),
            ],
          ),
          const SizedBox(height: 12),
          SelectableText(event.message),
          const SizedBox(height: 10),
          Text(
            '${formatTimestamp(event.unixMs)}  •  ${event.operationId}',
            style: Theme.of(
              context,
            ).textTheme.bodySmall?.copyWith(color: AppColors.textSecondary),
          ),
        ],
      ),
    );
  }
}

final class _Badge extends StatelessWidget {
  const _Badge({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

String enumLabel(String value) {
  return value
      .replaceAll('webTransport', 'webtransport')
      .replaceAllMapped(
        RegExp(r'([a-z0-9])([A-Z])'),
        (match) => '${match[1]}_${match[2]}',
      )
      .toUpperCase();
}

String formatTimestamp(int unixMs) {
  final value = DateTime.fromMillisecondsSinceEpoch(unixMs).toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${two(value.hour)}:${two(value.minute)}:${two(value.second)}';
}
