const HostApiVersionValue currentHostApiVersion = HostApiVersionValue(
  major: 1,
  minor: 0,
);

final class HostApiVersionValue {
  const HostApiVersionValue({required this.major, required this.minor});

  final int major;
  final int minor;
}

enum HostApiCompatibility {
  compatible,
  unsupportedMajor,
  hostTooOld;

  static HostApiCompatibility evaluate({
    required HostApiVersionValue requested,
    required HostApiVersionValue provided,
  }) {
    if (requested.major < 0 ||
        requested.minor < 0 ||
        provided.major < 0 ||
        provided.minor < 0) {
      throw ArgumentError('Host API version components must be non-negative.');
    }
    if (requested.major != provided.major) {
      return unsupportedMajor;
    }
    if (provided.minor < requested.minor) {
      return hostTooOld;
    }
    return compatible;
  }
}
