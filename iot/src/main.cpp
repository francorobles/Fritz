#include <chrono>
#include <csignal>
#include <iostream>
#include <thread>

#include "fritz/device.hpp"

namespace {
volatile std::sig_atomic_t g_running = 1;
void handle_signal(int) { g_running = 0; }
}  // namespace

int main() {
    std::signal(SIGINT, handle_signal);
    std::signal(SIGTERM, handle_signal);

    const fritz::Device device;
    std::cout << "Fritz IoT starting: " << device.status() << '\n';
#ifdef FRITZ_IOT_ENABLE_GPIO
    std::cout << "GPIO: enabled (no pins configured yet)\n";
#else
    std::cout << "GPIO: disabled (build with -DFRITZ_IOT_ENABLE_GPIO=ON on the Pi)\n";
#endif

    while (g_running) {
        // Placeholder heartbeat; replace with sensor/API work.
        std::this_thread::sleep_for(std::chrono::seconds(5));
        std::cout << "heartbeat: " << device.status() << '\n' << std::flush;
    }

    std::cout << "Fritz IoT shutting down\n";
    return 0;
}
