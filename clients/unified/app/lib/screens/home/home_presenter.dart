import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import '../../design/app_theme.dart';

final class HomeStatusPresentation {
  const HomeStatusPresentation({
    required this.label,
    required this.detail,
    required this.color,
    required this.icon,
  });

  final String label;
  final String detail;
  final Color color;
  final IconData icon;
}

HomeStatusPresentation presentTunnelState(TunnelState state) {
  return switch (state) {
    TunnelState.disconnected => const HomeStatusPresentation(
      label: 'Отключено',
      detail: 'Трафик не направляется через NeProto',
      color: AppColors.textSecondary,
      icon: Icons.radio_button_unchecked,
    ),
    TunnelState.connecting => const HomeStatusPresentation(
      label: 'Подключение',
      detail: 'Устанавливаем HTTP/3 WebTransport',
      color: AppColors.warning,
      icon: Icons.sync,
    ),
    TunnelState.connected => const HomeStatusPresentation(
      label: 'Подключено',
      detail: 'Защищённый канал NP/2 активен',
      color: AppColors.success,
      icon: Icons.check_circle,
    ),
    TunnelState.reconnecting => const HomeStatusPresentation(
      label: 'Переподключение',
      detail: 'Восстанавливаем тот же HTTP/3 канал',
      color: AppColors.warning,
      icon: Icons.sync,
    ),
    TunnelState.disconnecting => const HomeStatusPresentation(
      label: 'Отключение',
      detail: 'Безопасно завершаем туннель',
      color: AppColors.warning,
      icon: Icons.hourglass_bottom,
    ),
    TunnelState.failed => const HomeStatusPresentation(
      label: 'Ошибка',
      detail: 'Соединение не установлено',
      color: AppColors.danger,
      icon: Icons.error,
    ),
    TunnelState.unknown => const HomeStatusPresentation(
      label: 'Состояние неизвестно',
      detail: 'Ожидаем ответ системного VPN-модуля',
      color: AppColors.textSecondary,
      icon: Icons.help_outline,
    ),
  };
}

String presentRate(int bytesPerSecond) {
  if (bytesPerSecond < 1024) {
    return '$bytesPerSecond Б/с';
  }
  if (bytesPerSecond < 1024 * 1024) {
    return '${(bytesPerSecond / 1024).toStringAsFixed(1)} КБ/с';
  }
  return '${(bytesPerSecond / (1024 * 1024)).toStringAsFixed(1)} МБ/с';
}
