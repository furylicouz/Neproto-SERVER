# NeProto Host

Typed Flutter-to-native boundary for the NeProto Windows and iOS clients.

The Pigeon source of truth is `pigeons/client_host_api.dart`. Regenerate the
Dart, Swift and C++ bindings from the repository root with:

```powershell
clients/unified/tool/generate-host-api.ps1
```

Generated files are committed and must all come from Pigeon 28.0.0. Flutter
never receives profile credentials, route tokens or raw packet data through
this API.

