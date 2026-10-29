#include "fritz/face_server.hpp"

#include <cerrno>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <sstream>

#include <arpa/inet.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

namespace fritz {
namespace {

bool parse_float(const std::string& text, float& out) {
    try {
        std::size_t used = 0;
        const float value = std::stof(text, &used);
        if (used != text.size() || !std::isfinite(value)) {
            return false;
        }
        out = value;
        return true;
    } catch (...) {
        return false;
    }
}

std::string trim(const std::string& text) {
    const std::size_t first = text.find_first_not_of(" \t\r\n");
    if (first == std::string::npos) {
        return "";
    }
    const std::size_t last = text.find_last_not_of(" \t\r\n");
    return text.substr(first, last - first + 1);
}

}  // namespace

bool parse_face_command(const std::string& text, FaceCommand& out) {
    std::istringstream input(trim(text));
    std::string verb;
    if (!(input >> verb)) {
        return false;
    }

    std::vector<std::string> args;
    for (std::string arg; input >> arg;) {
        args.push_back(arg);
    }

    if (verb == "emotion" && args.size() == 1) {
        Emotion emotion;
        if (!parse_emotion(args[0], emotion)) {
            return false;
        }
        out.type = FaceCommand::Type::Emotion;
        out.emotion = emotion;
        return true;
    }
    if (verb == "standby" && args.empty()) {
        out.type = FaceCommand::Type::Standby;
        return true;
    }
    if (verb == "gaze" && args.size() == 1 && args[0] == "off") {
        out.type = FaceCommand::Type::GazeOff;
        return true;
    }
    if (verb == "gaze" && args.size() == 2) {
        float x = 0.0f;
        float y = 0.0f;
        if (!parse_float(args[0], x) || !parse_float(args[1], y)) {
            return false;
        }
        out.type = FaceCommand::Type::Gaze;
        out.x = x;
        out.y = y;
        return true;
    }
    if (verb == "speaking" && args.size() == 1 &&
        (args[0] == "on" || args[0] == "off")) {
        out.type = FaceCommand::Type::Speaking;
        out.speaking = args[0] == "on";
        return true;
    }
    if (verb == "blink" && args.empty()) {
        out.type = FaceCommand::Type::Blink;
        return true;
    }
    return false;
}

FaceServer::FaceServer(int port) {
    fd_ = ::socket(AF_INET, SOCK_DGRAM, 0);
    if (fd_ < 0) {
        error_ = std::string("socket failed: ") + std::strerror(errno);
        return;
    }

    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<std::uint16_t>(port));
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (::bind(fd_, reinterpret_cast<const sockaddr*>(&addr), sizeof(addr)) != 0) {
        error_ = std::string("bind 127.0.0.1:") + std::to_string(port) +
                 " failed: " + std::strerror(errno);
        ::close(fd_);
        fd_ = -1;
        return;
    }

    const int flags = ::fcntl(fd_, F_GETFL, 0);
    if (flags < 0 || ::fcntl(fd_, F_SETFL, flags | O_NONBLOCK) != 0) {
        error_ = std::string("fcntl(O_NONBLOCK) failed: ") + std::strerror(errno);
        ::close(fd_);
        fd_ = -1;
        return;
    }

    sockaddr_in bound{};
    socklen_t len = sizeof(bound);
    if (::getsockname(fd_, reinterpret_cast<sockaddr*>(&bound), &len) == 0) {
        port_ = ntohs(bound.sin_port);
    } else {
        port_ = port;
    }
}

FaceServer::~FaceServer() {
    if (fd_ >= 0) {
        ::close(fd_);
    }
}

std::vector<FaceCommand> FaceServer::poll() {
    std::vector<FaceCommand> commands;
    if (fd_ < 0) {
        return commands;
    }
    char buffer[512];
    for (;;) {
        const ssize_t n = ::recv(fd_, buffer, sizeof(buffer) - 1, 0);
        if (n < 0) {
            break;  // EAGAIN: drained. Anything else: stop this frame too.
        }
        if (n == 0) {
            continue;
        }
        const std::string text(buffer, static_cast<std::size_t>(n));
        FaceCommand command;
        if (parse_face_command(text, command)) {
            commands.push_back(command);
        } else {
            std::cerr << "face: ignoring unknown command: " << trim(text) << '\n';
        }
    }
    return commands;
}

}  // namespace fritz
