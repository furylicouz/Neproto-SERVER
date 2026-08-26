#include "include/neproto_host/neproto_host_plugin_c_api.h"

#include <flutter/plugin_registrar_windows.h>

#include "neproto_host_plugin.h"

void NeprotoHostPluginCApiRegisterWithRegistrar(
    FlutterDesktopPluginRegistrarRef registrar) {
  neproto_host::NeprotoHostPlugin::RegisterWithRegistrar(
      flutter::PluginRegistrarManager::GetInstance()
          ->GetRegistrar<flutter::PluginRegistrarWindows>(registrar));
}
