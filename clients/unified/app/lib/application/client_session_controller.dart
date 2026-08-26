import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:neproto_host/neproto_host.dart';

import '../host/client_host.dart';

@immutable
final class ClientSessionState {
  const ClientSessionState({
    required this.loading,
    required this.ready,
    required this.status,
    this.capabilities,
    this.error,
  });

  factory ClientSessionState.initial() {
    return ClientSessionState(
      loading: false,
      ready: false,
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
  final HostCapabilities? capabilities;
  final TunnelStatus status;
  final HostError? error;

  ClientSessionState copyWith({
    bool? loading,
    bool? ready,
    HostCapabilities? capabilities,
    TunnelStatus? status,
    HostError? error,
    bool clearError = false,
  }) {
    return ClientSessionState(
      loading: loading ?? this.loading,
      ready: ready ?? this.ready,
      capabilities: capabilities ?? this.capabilities,
      status: status ?? this.status,
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

  ClientSessionState get state => _state;

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
      _setState(_state.copyWith(loading: false, ready: true, clearError: true));
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
