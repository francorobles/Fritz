#pragma once

#include <string>

namespace fritz {

// Minimal device identity/status reported on startup and (later) to the API.
struct Device {
    std::string name = "fritz-pi4b";
    std::string model = "Raspberry Pi 4B";

    // Human-readable one-line status, e.g. for logs / a future /status report.
    std::string status() const;
};

}  // namespace fritz
