// Unit tests for the UDP face-protocol parser and a loopback round-trip
// through a real FaceServer on an OS-assigned port. No display needed:
// neither the parser nor FaceServer touches SDL.
#include <cassert>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include "fritz/face_server.hpp"

namespace {

int failures = 0;

void check(bool ok, const char* what) {
    if (!ok) {
        std::cerr << "FAIL: " << what << '\n';
        ++failures;
    }
}

void expect_valid(const std::string& text, fritz::FaceCommand::Type type) {
    fritz::FaceCommand cmd;
    check(fritz::parse_face_command(text, cmd), text.c_str());
    check(cmd.type == type, text.c_str());
}

void expect_invalid(const std::string& text) {
    fritz::FaceCommand cmd;
    check(!fritz::parse_face_command(text, cmd), text.c_str());
}

void send_udp(int port, const std::string& text) {
    const int fd = ::socket(AF_INET, SOCK_DGRAM, 0);
    assert(fd >= 0);
    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<std::uint16_t>(port));
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    const ssize_t sent = ::sendto(fd, text.data(), text.size(), 0,
                                  reinterpret_cast<const sockaddr*>(&addr),
                                  sizeof(addr));
    assert(sent == static_cast<ssize_t>(text.size()));
    ::close(fd);
}

}  // namespace

int main() {
    using Type = fritz::FaceCommand::Type;

    // Valid commands, including trailing newlines as sent by nc/echo.
    {
        fritz::FaceCommand cmd;
        check(fritz::parse_face_command("emotion happy\n", cmd), "emotion happy");
        check(cmd.type == Type::Emotion && cmd.emotion == fritz::Emotion::Happy,
              "emotion happy value");
        check(fritz::parse_face_command("gaze -0.5 0.25", cmd), "gaze values");
        check(cmd.type == Type::Gaze && cmd.x == -0.5f && cmd.y == 0.25f,
              "gaze values match");
        check(fritz::parse_face_command("speaking on", cmd) && cmd.speaking,
              "speaking on");
        check(fritz::parse_face_command("speaking off", cmd) && !cmd.speaking,
              "speaking off");
    }
    expect_valid("standby", Type::Standby);
    expect_valid("gaze off", Type::GazeOff);
    expect_valid("blink", Type::Blink);
    expect_valid("  emotion   tired  ", Type::Emotion);

    // Unknown / malformed input is rejected, never crashes.
    expect_invalid("");
    expect_invalid("dance");
    expect_invalid("emotion");
    expect_invalid("emotion ecstatic");
    expect_invalid("emotion happy extra");
    expect_invalid("gaze 1");
    expect_invalid("gaze a b");
    expect_invalid("gaze 1 2 3");
    expect_invalid("gaze nan 0");
    expect_invalid("speaking yes");
    expect_invalid("blink now");

    // Loopback round-trip: valid datagrams arrive parsed, bad ones drop.
    fritz::FaceServer server(0);
    check(server.valid(), "server binds ephemeral port");
    if (server.valid()) {
        check(server.port() > 0, "ephemeral port assigned");
        send_udp(server.port(), "emotion focused");
        send_udp(server.port(), "bogus command");
        send_udp(server.port(), "gaze 1 -1");
        // UDP on loopback is effectively immediate; poll briefly.
        std::vector<fritz::FaceCommand> got;
        for (int i = 0; i < 200 && got.size() < 2; ++i) {
            auto batch = server.poll();
            got.insert(got.end(), batch.begin(), batch.end());
            if (got.size() < 2) {
                ::usleep(5000);
            }
        }
        check(got.size() == 2, "two valid commands delivered");
        if (got.size() == 2) {
            check(got[0].type == Type::Emotion &&
                      got[0].emotion == fritz::Emotion::Focused,
                  "first command is emotion focused");
            check(got[1].type == Type::Gaze && got[1].x == 1.0f &&
                      got[1].y == -1.0f,
                  "second command is gaze 1 -1");
        }
    }

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "face_server_test: all checks passed\n";
    return 0;
}
