import 'package:flutter/widgets.dart';

import 'host/pigeon_client_host.dart';
import 'neproto_app.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(NeprotoApp(host: PigeonClientHost()));
}
