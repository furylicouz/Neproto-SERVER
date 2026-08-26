import 'package:flutter/material.dart';
import 'package:neproto_host/neproto_host.dart';

import '../../design/app_theme.dart';

final class ProfileTile extends StatelessWidget {
  const ProfileTile({
    required this.profile,
    required this.busy,
    required this.onSelect,
    required this.onRemove,
    super.key,
  });

  final ProfileSummary profile;
  final bool busy;
  final Future<void> Function(String id) onSelect;
  final Future<void> Function(String id) onRemove;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: profile.selected ? AppColors.accentMuted : AppColors.surface,
        border: Border.all(
          color: profile.selected ? AppColors.accent : AppColors.border,
        ),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            Row(
              children: <Widget>[
                Icon(
                  profile.selected ? Icons.check_circle : Icons.circle_outlined,
                  color: profile.selected
                      ? AppColors.accent
                      : AppColors.textSecondary,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: <Widget>[
                      Text(
                        profile.displayName,
                        style: const TextStyle(fontWeight: FontWeight.w700),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        '${profile.serverIdentity} · HTTP/3',
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(color: AppColors.textSecondary),
                      ),
                    ],
                  ),
                ),
                IconButton(
                  key: ValueKey<String>('remove-${profile.id}'),
                  tooltip: 'Удалить ${profile.displayName}',
                  onPressed: busy ? null : () => onRemove(profile.id),
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
            if (!profile.selected)
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  key: ValueKey<String>('select-${profile.id}'),
                  onPressed: busy ? null : () => onSelect(profile.id),
                  child: const Text('Выбрать'),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
