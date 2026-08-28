# NeProto for iOS

Нативный SwiftUI-клиент системного VPN для Neproto Chameleon NP/2.

## Data plane

```text
iOS apps → utun → userspace IPv4/IPv6 TCP+UDP → encrypted NP/2
         → HTTP/3, WebRTC, or HTTPS carrier → NP/2 server → target service
```

iOS-клиент не поднимает локальный SOCKS5 и не использует `hev-socks5-tunnel`.
TCP-потоки из `utun` напрямую открывают логические NP/2 streams. После
аутентификации каждая NP/2 cell дополнительно защищена направленным
ChaCha20-Poly1305 независимо от внешнего TLS/DTLS carrier.

## Возможности

- несколько серверных профилей;
- performance-first HTTPS/WebSocket с HTTP/3/WebTransport и WebRTC fallback;
- системный VPN для TCP- и UDP-трафика IPv4 и IPv6;
- DNS внутри зашифрованного NP/2 data plane с краткоживущей in-memory
  атрибуцией доменов для правил Domain и GeoSite;
- хранение 256-битного root secret в iOS Keychain;
- сборка для физического iPhone и Apple Silicon Simulator;
- диагностика состояния NP/2 без логирования ключей, destinations и payload.

NP/2 v2.2 передаёт UDP через надёжные association records и, когда carrier и
сервер согласовали capability, через отдельный ChaCha20-Poly1305 datagram
fast-path. Большие datagrams автоматически используют надёжный путь; очереди,
повторы и время жизни association ограничены.

## Сборка

Требования: macOS, Xcode 26, Go 1.26.7 и Apple Developer Team с разрешённой
Network Extensions capability.

```bash
cd /path/to/Neproto
clients/ios/Scripts/bootstrap-macos.sh
open clients/ios/NeProto.xcodeproj
```

Скрипты фиксируют версии `gomobile`, XcodeGen и Go-зависимостей. Единственный
нативный внешний артефакт Packet Tunnel — `NP2Mobile.xcframework`.

В Xcode выберите одну Developer Team для целей `NeProto` и `PacketTunnel`,
подключите iPhone и нажмите Run. Обе цели должны иметь Packet Tunnel capability.

## Добавление сервера

В приложении нажмите «Добавить сервер» и укажите:

1. DNS-домен, например `neproto.lyntragram.ru`.
2. Публичный IP сервера; приложение не содержит зашитых адресов.
3. Приватные HTTPS/WSS, WebRTC signaling и HTTP/3 routes.
4. Канонический 256-битный пользовательский secret в base64url без `=`.

Предпочтительный способ — отсканировать v2 QR из `np` / `neprotoctl user
export`. Старые v1 QR продолжают импортироваться без HTTP/3. Новые v2 QR
передают HTTP/3 route, политику `require_datagrams` и безопасный предел
параллельных carrier-соединений.

Настраивать ускорение вручную не нужно. Все старые и новые мобильные
`performance`-профили автоматически используют адаптивный режим: два carrier-
соединения прогреваются в фоне, третье создаётся только под нагрузкой и
закрывается после простоя. Каждое соединение независимо проходит NP/2
аутентификацию, согласование v2.2 и обязательное шифрование; один TCP-поток
между соединениями не дробится. Производственная конфигурация использует
`cover_mode=off`: Mosaic не добавляет задержки, padding и dummy cells, но его
можно явно включить для лабораторных сравнений.

Routes начинаются с `/`, имеют длину не менее 16 символов и не совпадают.
Root secret не попадает в UserDefaults, provider payload, проект или логи.

## Проверки

```bash
cd clients/ios/Core && swift test
cd ..
xcodebuild -project NeProto.xcodeproj -scheme NeProto \
  -configuration Debug -sdk iphonesimulator CODE_SIGNING_ALLOWED=NO build
xcodebuild -project NeProto.xcodeproj -scheme NeProto \
  -configuration Debug -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
```

Работу Network Extension проверяют только на подписанном физическом iPhone.
Simulator подтверждает компиляцию и линковку, но не запускает системный Packet
Tunnel.

## Network migration

`PacketTunnelProvider` handles Wi-Fi/cellular path changes outside the stop
queue. It first sends an authenticated NP/2 `PING/PONG` over the current
session. A surviving carrier stays active. Otherwise the client performs
bounded automatic carrier selection, atomically switches new TUN flows to the
replacement session, and drains existing streams on the previous session for
at most 30 seconds. A reconnect failure does not tear down a still-live old
session, and `stopTunnel` cancels migration immediately instead of waiting for
its timeout.

Initial connection is connectivity-first: HTTPS may establish the tunnel while
HTTP/3 is still authenticating. When HTTPS is primary, a bounded background
probe gives HTTP/3 and then WebRTC a complete attempt without exposing a mode
selector to the user. A successfully authenticated faster carrier is promoted
automatically; new TCP and UDP flows use it while existing streams drain on the
previous carrier. HTTPS remains the compatibility path when native datagram
carriers are unavailable.

The app diagnostics expose aggregate network-change, reconnect, and migration
counters without server routes, credentials, destinations, or payloads. This
behavior must be validated on a signed physical iPhone; Simulator and unsigned
iphoneos builds prove compilation/linking only.
