#ifndef FLUTTER_PLUGIN_NEPROTO_WINDOWS_REPLY_DISPATCHER_H_
#define FLUTTER_PLUGIN_NEPROTO_WINDOWS_REPLY_DISPATCHER_H_

#include <flutter/plugin_registrar_windows.h>

#include <memory>

#include "async_client_host.h"

namespace neproto_host {

std::shared_ptr<ReplyDispatcher> CreateWindowsReplyDispatcher(
    flutter::PluginRegistrarWindows* registrar);

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_WINDOWS_REPLY_DISPATCHER_H_
