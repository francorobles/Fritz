#include "fritz/device.hpp"

namespace fritz {

std::string Device::status() const {
    return name + " (" + model + "): ok";
}

}  // namespace fritz
