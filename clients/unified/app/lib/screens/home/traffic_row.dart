import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import '../../design/app_theme.dart';
import 'home_presenter.dart';

final class TrafficRow extends StatelessWidget {
  const TrafficRow({required this.status, super.key});

  final TunnelStatus status;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 18),
      child: Row(
        children: <Widget>[
          _Metric(
            label: 'СКАЧИВАНИЕ',
            value: presentRate(status.downloadBytesPerSecond),
          ),
          const SizedBox(height: 48, child: VerticalDivider()),
          _Metric(
            label: 'ОТПРАВКА',
            value: presentRate(status.uploadBytesPerSecond),
          ),
          const SizedBox(height: 48, child: VerticalDivider()),
          const _Metric(label: 'КАНАЛ', value: 'HTTP/3'),
        ],
      ),
    );
  }
}

final class _Metric extends StatelessWidget {
  const _Metric({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Column(
        children: <Widget>[
          Text(
            label,
            style: const TextStyle(
              color: AppColors.textSecondary,
              fontSize: 10,
              letterSpacing: 0.8,
            ),
          ),
          const SizedBox(height: 6),
          Text(value, style: const TextStyle(fontWeight: FontWeight.w700)),
        ],
      ),
    );
  }
}
