import 'package:flutter/material.dart';

abstract final class AppColors {
  static const background = Color(0xFF080D14);
  static const surface = Color(0xFF111925);
  static const surfaceRaised = Color(0xFF172131);
  static const border = Color(0xFF273449);
  static const accent = Color(0xFF4F8CFF);
  static const accentMuted = Color(0xFF172C50);
  static const success = Color(0xFF45C58B);
  static const warning = Color(0xFFFFB454);
  static const danger = Color(0xFFFF5364);
  static const textPrimary = Color(0xFFF5F7FB);
  static const textSecondary = Color(0xFFA9B5C7);
}

abstract final class AppTheme {
  static ThemeData dark() {
    const scheme = ColorScheme.dark(
      primary: AppColors.accent,
      onPrimary: Colors.white,
      surface: AppColors.surface,
      onSurface: AppColors.textPrimary,
      error: AppColors.danger,
      onError: Colors.white,
      outline: AppColors.border,
    );
    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.dark,
      colorScheme: scheme,
      scaffoldBackgroundColor: AppColors.background,
      fontFamily: 'Segoe UI',
      textTheme: const TextTheme(
        headlineMedium: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 28,
          fontWeight: FontWeight.w700,
          letterSpacing: -0.6,
        ),
        titleLarge: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 20,
          fontWeight: FontWeight.w700,
        ),
        bodyLarge: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 16,
          height: 1.4,
        ),
        bodyMedium: TextStyle(
          color: AppColors.textSecondary,
          fontSize: 14,
          height: 1.4,
        ),
        labelLarge: TextStyle(fontSize: 15, fontWeight: FontWeight.w700),
      ),
      dividerColor: AppColors.border,
      focusColor: AppColors.accent.withValues(alpha: 0.28),
    );
  }
}
