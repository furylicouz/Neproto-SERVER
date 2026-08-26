import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart' as host;

import '../../design/app_theme.dart';
import 'diagnostic_event_tile.dart';

final class DiagnosticsScreen extends StatelessWidget {
  const DiagnosticsScreen({
    required this.snapshot,
    required this.loading,
    required this.onRefresh,
    super.key,
  });

  final host.DiagnosticsSnapshot? snapshot;
  final bool loading;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 880),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: <Widget>[
                  Wrap(
                    alignment: WrapAlignment.spaceBetween,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    spacing: 16,
                    runSpacing: 12,
                    children: <Widget>[
                      Text(
                        'Диагностика',
                        style: Theme.of(context).textTheme.headlineMedium,
                      ),
                      FilledButton.tonalIcon(
                        key: const ValueKey<String>('refresh-diagnostics'),
                        onPressed: loading ? null : onRefresh,
                        icon: loading
                            ? const SizedBox.square(
                                dimension: 18,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              )
                            : const Icon(Icons.refresh),
                        label: const Text('Обновить'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    'Безопасный снимок native host без ключей и строк импорта.',
                    style: TextStyle(color: AppColors.textSecondary),
                  ),
                  const SizedBox(height: 20),
                  if (snapshot == null)
                    _EmptyDiagnostics(loading: loading)
                  else ...<Widget>[
                    _Summary(snapshot: snapshot!),
                    const SizedBox(height: 24),
                    Text(
                      'События',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: 12),
                    if (snapshot!.events.isEmpty)
                      const _NoEvents()
                    else
                      ...snapshot!.events.reversed.map(
                        (event) => Padding(
                          padding: const EdgeInsets.only(bottom: 12),
                          child: DiagnosticEventTile(event: event),
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

final class _Summary extends StatelessWidget {
  const _Summary({required this.snapshot});

  final host.DiagnosticsSnapshot snapshot;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: AppColors.surface,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Wrap(
        spacing: 28,
        runSpacing: 18,
        children: <Widget>[
          _Fact(label: 'Политика', value: snapshot.carrierPolicy),
          _Fact(
            label: 'Транспорт',
            value: enumLabel(snapshot.currentCarrier.name),
          ),
          _Fact(label: 'Переподключения', value: '${snapshot.reconnectCount}'),
          _Fact(label: 'Приложение', value: 'App ${snapshot.appVersion}'),
          _Fact(label: 'Native host', value: 'Host ${snapshot.hostVersion}'),
          _Fact(label: 'Ядро', value: 'Core ${snapshot.coreVersion}'),
        ],
      ),
    );
  }
}

final class _Fact extends StatelessWidget {
  const _Fact({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 220,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Text(label, style: const TextStyle(color: AppColors.textSecondary)),
          const SizedBox(height: 3),
          SelectableText(
            value,
            style: const TextStyle(fontWeight: FontWeight.w700),
          ),
        ],
      ),
    );
  }
}

final class _EmptyDiagnostics extends StatelessWidget {
  const _EmptyDiagnostics({required this.loading});

  final bool loading;

  @override
  Widget build(BuildContext context) {
    return _EmptyCard(
      icon: loading ? Icons.sync : Icons.monitor_heart_outlined,
      title: loading ? 'Получаем диагностику' : 'Диагностика ещё не загружена',
    );
  }
}

final class _NoEvents extends StatelessWidget {
  const _NoEvents();

  @override
  Widget build(BuildContext context) {
    return const _EmptyCard(
      icon: Icons.check_circle_outline,
      title: 'Событий пока нет',
    );
  }
}

final class _EmptyCard extends StatelessWidget {
  const _EmptyCard({required this.icon, required this.title});

  final IconData icon;
  final String title;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 40),
      decoration: BoxDecoration(
        color: AppColors.surface,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        children: <Widget>[
          Icon(icon, size: 36, color: AppColors.textSecondary),
          const SizedBox(height: 10),
          Text(title, textAlign: TextAlign.center),
        ],
      ),
    );
  }
}
