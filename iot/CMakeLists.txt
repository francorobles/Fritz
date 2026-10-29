cmake_minimum_required(VERSION 3.16)
project(fritz_iot VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(FRITZ_IOT_ENABLE_GPIO "Build with Raspberry Pi GPIO support (stub until a GPIO backend is wired)" OFF)

find_package(SDL2 REQUIRED)

add_executable(fritz_iot
    src/main.cpp
    src/device.cpp
    src/app.cpp
    src/eyes.cpp
    src/face_server.cpp
)
target_include_directories(fritz_iot PRIVATE include)
target_link_libraries(fritz_iot PRIVATE SDL2::SDL2)
target_compile_options(fritz_iot PRIVATE -Wall -Wextra -Wpedantic)

include(CTest)
if(BUILD_TESTING)
    add_executable(fritz_face_test
        tests/face_server_test.cpp
        src/face_server.cpp
        src/eyes.cpp
    )
    target_include_directories(fritz_face_test PRIVATE include)
    target_link_libraries(fritz_face_test PRIVATE SDL2::SDL2)
    target_compile_options(fritz_face_test PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME face_protocol COMMAND fritz_face_test)
endif()

if(FRITZ_IOT_ENABLE_GPIO)
    target_compile_definitions(fritz_iot PRIVATE FRITZ_IOT_ENABLE_GPIO=1)
endif()

install(TARGETS fritz_iot RUNTIME DESTINATION bin)
