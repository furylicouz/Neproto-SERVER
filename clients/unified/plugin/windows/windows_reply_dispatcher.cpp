#include "windows_reply_dispatcher.h"

// This must be included before many other Windows headers.
#define NOMINMAX
#include <windows.h>

#include <deque>
#include <functional>
#include <mutex>
#include <optional>
#include <utility>

namespace neproto_host {
namespace {

constexpr std::size_t kMaxPendingReplies = 128;

class WindowsReplyDispatcher final : public ReplyDispatcher {
 public:
  explicit WindowsReplyDispatcher(flutter::PluginRegistrarWindows* registrar)
      : registrar_(registrar),
        window_(RootWindow(registrar)),
        message_(RegisterWindowMessageW(L"NeProto.Host.Reply.v1")) {
    if (registrar_ && window_ && message_ != 0) {
      delegate_id_ = registrar_->RegisterTopLevelWindowProcDelegate(
          [this](HWND window, UINT message, WPARAM, LPARAM)
              -> std::optional<LRESULT> {
            if (window != window_ || message != message_) return std::nullopt;
            Drain();
            return LRESULT{0};
          });
    }
  }

  ~WindowsReplyDispatcher() override {
    if (registrar_ && delegate_id_ != 0) {
      registrar_->UnregisterTopLevelWindowProcDelegate(delegate_id_);
    }
    std::lock_guard<std::mutex> lock(mutex_);
    stopping_ = true;
    tasks_.clear();
  }

  bool Post(std::function<void()> task) override {
    if (!task || !window_ || message_ == 0 || delegate_id_ == 0) return false;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      if (stopping_ || tasks_.size() >= kMaxPendingReplies) return false;
      tasks_.push_back(std::move(task));
    }
    return PostMessageW(window_, message_, 0, 0) != FALSE;
  }

 private:
  static HWND RootWindow(flutter::PluginRegistrarWindows* registrar) {
    if (!registrar || !registrar->GetView()) return nullptr;
    HWND view = registrar->GetView()->GetNativeWindow();
    HWND root = view ? GetAncestor(view, GA_ROOT) : nullptr;
    return root ? root : view;
  }

  void Drain() {
    std::deque<std::function<void()>> tasks;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      tasks.swap(tasks_);
    }
    for (auto& task : tasks) task();
  }

  flutter::PluginRegistrarWindows* registrar_;
  HWND window_;
  UINT message_;
  int delegate_id_ = 0;
  std::mutex mutex_;
  std::deque<std::function<void()>> tasks_;
  bool stopping_ = false;
};

}  // namespace

std::shared_ptr<ReplyDispatcher> CreateWindowsReplyDispatcher(
    flutter::PluginRegistrarWindows* registrar) {
  return std::make_shared<WindowsReplyDispatcher>(registrar);
}

}  // namespace neproto_host
