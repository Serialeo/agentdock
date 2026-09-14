#pragma once

#include <cmath>
#include <stdexcept>

namespace computer_spike {
struct Point { double x, y; };
struct Rect { double x, y, width, height; };
struct Geometry {
    int width, height; // 编码图像尺寸；DXGI 旋转另在图像编码前处理。
    Rect desktop;

    void validate() const {
        if (width < 1 || height < 1 || !std::isfinite(desktop.x) ||
            !std::isfinite(desktop.y) || !std::isfinite(desktop.width) ||
            !std::isfinite(desktop.height) || desktop.width <= 0 || desktop.height <= 0)
            throw std::invalid_argument("invalid capture geometry");
    }
    Point to_native(Point p) const {
        validate();
        if (!std::isfinite(p.x) || !std::isfinite(p.y) || p.x < 0 || p.y < 0 ||
            p.x >= width || p.y >= height) throw std::invalid_argument("image point out of bounds");
        return {desktop.x + (p.x + .5) * desktop.width / width,
                desktop.y + (p.y + .5) * desktop.height / height};
    }
    Point to_image(Point p) const {
        validate();
        if (!std::isfinite(p.x) || !std::isfinite(p.y) || p.x < desktop.x || p.y < desktop.y ||
            p.x >= desktop.x + desktop.width || p.y >= desktop.y + desktop.height)
            throw std::invalid_argument("native point out of bounds");
        return {(p.x - desktop.x) * width / desktop.width - .5,
                (p.y - desktop.y) * height / desktop.height - .5};
    }
};
inline long absolute_input(double coordinate, double origin, double span) {
    if (!std::isfinite(coordinate) || !std::isfinite(origin) || !std::isfinite(span) || span < 2 ||
        coordinate < origin || coordinate >= origin + span)
        throw std::invalid_argument("invalid virtual desktop coordinate");
    // SendInput 的端点对应整个 virtual desktop；先落到物理像素，避免越界截断。
    return std::lround((std::floor(coordinate) - origin) * 65535.0 / (span - 1));
}
} // namespace computer_spike
