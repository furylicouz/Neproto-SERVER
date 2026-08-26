import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:neproto_host/neproto_host.dart';

import '../host/client_host.dart';

@immutable
final class ClientSessionState {
  const ClientSessionState({
    required this.loading,
    required this.ready,
    required this.commandPending,
    required this.status,
    this.profiles = const <ProfileSummary>[],
    this.capabilities,
    this.error,
  });

  factory ClientSessionState.initial() {
    return ClientSessionState(
      loading: false,
      ready: false,
      commandPending: false,
      status: TunnelStatus(
        state: TunnelState.disconnected,
        carrier: CarrierKind.none,
        connectedAtUnixMs: 0,
        uploadBytesPerSecond: 0,
        downloadBytesPerSecond: 0,
        uploadTotalBytes: 0,
        downloadTotalBytes: 0,
        sequence: 0,
      ),
    );
  }

  final bool loading;
  final bool ready;
  final bool commandPending;
  final HostCapabilities? capabilities;
  final TunnelStatus status;
  final List<ProfileSummary> profiles;
  final HostError? error;

  ProfileSummary? get selectedProfile {
    for (final profile in profiles) {
      if (profile.selected) {
        return profile;
      }
    }
    return null;
  }

  ClientSessionState copyWith({
    bool? loading,
    bool? ready,
    bool? commandPending,
    HostCapabilities? capabilities,
    TunnelStatus? status,
    List<ProfileSummary>? profiles,
    HostError? error,
    bool clearError = false,
  }) {
    return ClientSessionState(
      loading: loading ?? this.loading,
      ready: ready ?? this.ready,
      commandPending: commandPending ?? this.commandPending,
      capabilities: capabilities ?? this.capabilities,
      status: status ?? this.status,
      profiles: profiles ?? this.profiles,
      error: clearError ? null : error ?? this.error,
    );
  }
}

final class ClientSessionController extends ChangeNotifier {
  ClientSessionController(this._host);

  final ClientHost _host;
  ClientSessionState _state = ClientSessionState.initial();
  StreamSubscription<TunnelStatus>? _statusSubscription;
  bool _started = false;
  bool _disposed = false;
  int _operationSequence = 0;

  ClientSessionState get state => _state;

  Future<void> connect(String profileId) async {
    if (!_state.ready ||
        _state.commandPending ||
        (_state.status.state != TunnelState.disconnected &&
            _state.status.state != TunnelState.failed)) {
      return;
    }
    if (!_validProfileId(profileId)) {
      throw ArgumentError.value(profileId, 'profileId', 'invalid profile ID');
    }
    final operationId = _nextOperationId('connect');
    _setState(_state.copyWith(commandPending: true, clearError: true));
    try {
      _applyStatus(
        await _host.connect(
          ConnectRequest(profileId: profileId, operationId: operationId),
        ),
      );
    } catch (_) {
      _setCommandError(operationId);
    } finally {
      _setState(_state.copyWith(commandPending: false));
    }
  }

  Future<void> disconnect() async {
    if (!_state.ready ||
        _state.commandPending ||
        _state.status.state != TunnelState.connected) {
      return;
    }
    final operationId = _nextOperationId('disconnect');
    _setState(_state.copyWith(commandPending: true, clearError: true));
    try {
      _applyStatus(
        await _host.disconnect(DisconnectRequest(operationId: operationId)),
      );
    } catch (_) {
      _setCommandError(operationId);
    } finally {
      _setState(_state.copyWith(commandPending: false));
    }
  }

  Future<bool> importProfile(String onboardingValue) async {
    if (!_state.ready || _state.commandPending) {
      return false;
    }
    try {
      HostInputValidator.validateOnboardingValue(onboardingValue);
    } on ArgumentError {
      _setProfileValidationError('import-invalid');
      return false;
    }
    final operationId = _nextOperationId('import');
    _setState(_state.copyWith(commandPending: true, clearError: true));
    try {
      final imported = await _host.importProfile(
        ImportProfileRequest(
          onboardingValue: onboardingValue,
          operationId: operationId,
        ),
      );
      _setState(
        _state.copyWith(
          profiles: List<ProfileSummary>.unmodifiable(<ProfileSummary>[
            ..._state.profiles.where((item) => item.id != imported.id),
            imported,
          ]),
        ),
      );
      return true;
    } catch (_) {
      _setCommandError(operationId);
      return false;
    } finally {
      _setState(_state.copyWith(commandPending: false));
    }
  }

  Future<void> selectProfile(String profileId) async {
    if (!_state.ready || _state.commandPending || !_validProfileId(profileId)) {
      return;
    }
    final operationId = _nextOperationId('select');
    _setState(_state.copyWith(commandPending: true, clearError: true));
    try {
      final selected = await _host.selectProfile(
        SelectProfileRequest(profileId: profileId, operationId: operationId),
      );
      _setState(
        _state.copyWith(
          profiles: List<ProfileSummary>.unmodifiable(
            _state.profiles.map(
              (item) => _copyProfile(
                item.id == selected.id ? selected : item,
                selected: item.id == selected.id,
              ),
            ),
          ),
        ),
      );
    } catch (_) {
      _setCommandError(operationId);
    } finally {
      _setState(_state.copyWith(commandPending: false));
    }
  }

  Future<void> removeProfile(String profileId) async {
    if (!_state.ready || _state.commandPending || !_validProfileId(profileId)) {
      return;
    }
    if (_state.status.profileId == profileId &&
        _state.status.state != TunnelState.disconnected &&
        _state.status.state != TunnelState.failed) {
      return;
    }
    final operationId = _nextOperationId('remove');
    _setState(_state.copyWith(commandPending: true, clearError: true));
    try {
      await _host.removeProfile(
        RemoveProfileRequest(
          profileId: profileId,
          force: false,
          operationId: operationId,
        ),
      );
      _setState(
        _state.copyWith(
          profiles: List<ProfileSummary>.unmodifiable(
            _state.profiles.where((item) => item.id != profileId),
          ),
        ),
      );
    } catch (_) {
      _setCommandError(operationId);
    } finally {
      _setState(_state.copyWith(commandPending: false));
    }
  }

  Future<void> start() async {
    if (_started || _disposed) {
      return;
    }
    _started = true;
    _statusSubscription = _host.statusChanges.listen(_applyStatus);
    _setState(_state.copyWith(loading: true, clearError: true));
    try {
      final capabilities = await _host.getCapabilities(
        HostApiVersion(
          major: currentHostApiVersion.major,
          minor: currentHostApiVersion.minor,
        ),
      );
      final compatibility = HostApiCompatibility.evaluate(
        requested: currentHostApiVersion,
        provided: HostApiVersionValue(
          major: capabilities.apiVersion.major,
          minor: capabilities.apiVersion.minor,
        ),
      );
      if (compatibility != HostApiCompatibility.compatible ||
          !capabilities.supportsHttp3WebTransport) {
        _setState(
          _state.copyWith(
            loading: false,
            ready: false,
            capabilities: capabilities,
            error: _unsupportedHostError(),
          ),
        );
        return;
      }
      _setState(_state.copyWith(capabilities: capabilities));
      _applyStatus(await _host.getStatus());
      final profiles = await _host.listProfiles();
      _setState(
        _state.copyWith(
          loading: false,
          ready: true,
          profiles: List<ProfileSummary>.unmodifiable(profiles),
          clearError: true,
        ),
      );
    } catch (_) {
      _setState(
        _state.copyWith(
          loading: false,
          ready: false,
          error: HostError(
            code: HostErrorCode.hostUnavailable,
            stage: ErrorStage.hostIpc,
            message: 'Native host is unavailable.',
            retryable: true,
            operationId: 'startup',
          ),
        ),
      );
    }
  }

  void _applyStatus(TunnelStatus status) {
    HostInputValidator.validateStatusSequence(status.sequence);
    if (status.sequence < _state.status.sequence) {
      return;
    }
    _setState(
      _state.copyWith(
        status: status,
        error: status.lastError,
        clearError: status.lastError == null,
      ),
    );
  }

  void _setState(ClientSessionState next) {
    if (_disposed) {
      return;
    }
    _state = next;
    notifyListeners();
  }

  String _nextOperationId(String action) {
    _operationSequence++;
    return '$action-$_operationSequence';
  }

  void _setCommandError(String operationId) {
    _setState(
      _state.copyWith(
        error: HostError(
          code: HostErrorCode.hostUnavailable,
          stage: ErrorStage.hostIpc,
          message: 'Native host command failed.',
          retryable: true,
          operationId: operationId,
        ),
      ),
    );
  }

  void _setProfileValidationError(String operationId) {
    _setState(
      _state.copyWith(
        error: HostError(
          code: HostErrorCode.invalidProfile,
          stage: ErrorStage.profileValidation,
          message: 'Profile import value is invalid.',
          retryable: false,
          operationId: operationId,
        ),
      ),
    );
  }

  @override
  void dispose() {
    if (_disposed) {
      return;
    }
    _disposed = true;
    unawaited(_statusSubscription?.cancel());
    _host.dispose();
    super.dispose();
  }
}

HostError _unsupportedHostError() {
  return HostError(
    code: HostErrorCode.unsupportedApiVersion,
    stage: ErrorStage.hostNegotiation,
    message: 'Host API version is unsupported.',
    retryable: false,
    operationId: 'startup',
  );
}

bool _validProfileId(String value) {
  return value.isNotEmpty &&
      value == value.trim() &&
      utf8.encode(value).length <= 128;
}

ProfileSummary _copyProfile(ProfileSummary source, {required bool selected}) {
  return ProfileSummary(
    id: source.id,
    displayName: source.displayName,
    serverIdentity: source.serverIdentity,
    host: source.host,
    selected: selected,
    hasCredential: source.hasCredential,
    origin: source.origin,
    catalogManaged: source.catalogManaged,
    updatedAtUnixMs: source.updatedAtUnixMs,
  );
}
