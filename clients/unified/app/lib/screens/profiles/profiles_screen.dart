import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import '../../design/app_theme.dart';
import 'import_profile_dialog.dart';
import 'profile_tile.dart';

final class ProfilesScreen extends StatelessWidget {
  const ProfilesScreen({
    required this.profiles,
    required this.busy,
    required this.onSelect,
    required this.onRemove,
    required this.onImport,
    super.key,
  });

  final List<ProfileSummary> profiles;
  final bool busy;
  final Future<void> Function(String id) onSelect;
  final Future<void> Function(String id) onRemove;
  final Future<bool> Function(String value) onImport;

  @override
  Widget build(BuildContext context) {
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
                  Wrap(
                    alignment: WrapAlignment.spaceBetween,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    spacing: 16,
                    runSpacing: 12,
                    children: <Widget>[
                      Text(
                        'Профили',
                        style: Theme.of(context).textTheme.headlineMedium,
                      ),
                      FilledButton.icon(
                        key: const ValueKey<String>('import-profile'),
                        onPressed: busy
                            ? null
                            : () => showImportProfileDialog(
                                context,
                                onImport: onImport,
                              ),
                        icon: const Icon(Icons.add),
                        label: const Text('Добавить профиль'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    'Профили хранятся системным VPN-модулем. Секреты не передаются в Flutter.',
                    style: TextStyle(color: AppColors.textSecondary),
                  ),
                  const SizedBox(height: 24),
                  if (profiles.isEmpty)
                    const _EmptyProfiles()
                  else
                    ...profiles.map(
                      (profile) => Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: ProfileTile(
                          profile: profile,
                          busy: busy,
                          onSelect: onSelect,
                          onRemove: onRemove,
                        ),
                      ),
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

final class _EmptyProfiles extends StatelessWidget {
  const _EmptyProfiles();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 48),
      decoration: BoxDecoration(
        color: AppColors.surface,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(16),
      ),
      child: const Column(
        children: <Widget>[
          Icon(
            Icons.vpn_key_outlined,
            size: 40,
            color: AppColors.textSecondary,
          ),
          SizedBox(height: 12),
          Text('Нет профилей', style: TextStyle(fontWeight: FontWeight.w700)),
          SizedBox(height: 4),
          Text(
            'Добавьте выданную строку импорта, чтобы подключиться.',
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}
