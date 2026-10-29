#pragma once

#include <string>
#include <vector>

#include "fritz/eyes.hpp"

namespace fritz {

// One parsed UDP face-protocol command (see poll()).
struct FaceCommand {
    enum class Type { Emotion, Standby, Gaze, GazeOff, Speaking, Blink };

    Type type = Type::Standby;
    Emotion emotion = Emotion::Normal;  // Emotion only
    float x = 0.0f;                     // Gaze only, [-1, 1]
    float y = 0.0f;                     // Gaze only, [-1, 1]
    bool speaking = false;              // Speaking only
};

// Parses one datagram's text into a command. Returns false (and leaves
// `out` unspecified) on unknown or malformed input; callers log and drop
// it. Pure: exposed for unit tests, no socket involved.
bool parse_face_command(const std::string& text, FaceCommand& out);

// Non-blocking UDP listener for the face protocol on 127.0.0.1:<port>
// (one ASCII command per datagram). No threads: the app calls poll()
// once per frame. Bad datagrams are logged and ignored, never fatal.
// Port 0 lets the OS pick a free port (see port()).
class FaceServer {
public:
    explicit FaceServer(int port);
    ~FaceServer();

    FaceServer(const FaceServer&) = delete;
    FaceServer& operator=(const FaceServer&) = delete;

    bool valid() const { return fd_ >= 0; }
    std::string error() const { return error_; }
    int port() const { return port_; }

    // Drains every pending datagram and returns the valid commands in
    // arrival order. Empty when nothing arrived (or when !valid()).
    std::vector<FaceCommand> poll();

private:
    int fd_ = -1;  // POSIX socket descriptor; socket headers stay in the .cpp
    int port_ = 0;
    std::string error_;
};

}  // namespace fritz
