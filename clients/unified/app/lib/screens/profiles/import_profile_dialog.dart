import 'package:flutter/material.dart';

import '../../design/app_theme.dart';

Future<void> showImportProfileDialog(
  BuildContext context, {
  required Future<String?> Function(String value) onImport,
}) {
  return showDialog<void>(
    context: context,
    builder: (context) => _ImportProfileDialog(onImport: onImport),
  );
}

final class _ImportProfileDialog extends StatefulWidget {
  const _ImportProfileDialog({required this.onImport});

  final Future<String?> Function(String value) onImport;

  @override
  State<_ImportProfileDialog> createState() => _ImportProfileDialogState();
}

final class _ImportProfileDialogState extends State<_ImportProfileDialog> {
  final TextEditingController _controller = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _controller.clear();
    _controller.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_busy) {
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final error = await widget.onImport(_controller.text);
    if (!mounted) {
      return;
    }
    if (error == null) {
      _controller.clear();
      Navigator.of(context).pop();
      return;
    }
    setState(() {
      _busy = false;
      _error = error;
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: AppColors.surfaceRaised,
      title: const Text('Добавить профиль'),
      content: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: <Widget>[
            const Text(
              'Вставьте одноразовую строку np2://import. Она не сохраняется в приложении.',
            ),
            const SizedBox(height: 16),
            TextField(
              key: const ValueKey<String>('onboarding-input'),
              controller: _controller,
              enabled: !_busy,
              autofocus: true,
              autocorrect: false,
              enableSuggestions: false,
              maxLines: 3,
              decoration: InputDecoration(
                labelText: 'Строка импорта',
                errorText: _error,
                border: const OutlineInputBorder(),
              ),
              onSubmitted: (_) => _submit(),
            ),
          ],
        ),
      ),
      actions: <Widget>[
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(),
          child: const Text('Отмена'),
        ),
        FilledButton(
          onPressed: _busy ? null : _submit,
          child: _busy
              ? const SizedBox.square(
                  dimension: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Импортировать'),
        ),
      ],
    );
  }
}
