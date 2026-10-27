cmake_minimum_required(VERSION 3.16)
project(fritz_iot VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(FRITZ_IOT_ENABLE_GPIO "Build with Raspberry Pi GPIO support (stub until a GPIO backend is wired)" OFF)

add_executable(fritz_iot
    src/main.cpp
    src/device.cpp
)
target_include_directories(fritz_iot PRIVATE include)
target_compile_options(fritz_iot PRIVATE -Wall -Wextra -Wpedantic)

if(FRITZ_IOT_ENABLE_GPIO)
    target_compile_definitions(fritz_iot PRIVATE FRITZ_IOT_ENABLE_GPIO=1)
endif()

install(TARGETS fritz_iot RUNTIME DESTINATION bin)
