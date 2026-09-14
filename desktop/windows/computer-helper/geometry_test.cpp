#include "geometry.hpp"
#include <cassert>
#include <initializer_list>
#include <limits>

using namespace computer_spike;
int main() {
    // 左侧副屏、缩小截图和非整数比例，独立检查已知实际坐标。
    Geometry g{1280, 720, {-2560, -100, 2560, 1440}};
    auto p = g.to_native({100, 50});
    assert(p.x == -2359 && p.y == 1);
    auto image = g.to_image({-2359, 1});
    assert(image.x == 100 && image.y == 50);
    Geometry portrait{900, 1600, {1920, -200, 1080, 1920}};
    p = portrait.to_native({449.5, 799.5});
    assert(p.x == 2460 && p.y == 760);
    assert(absolute_input(-2560, -2560, 4480) == 0);
    assert(absolute_input(1919, -2560, 4480) == 65535);
    for (Point invalid : {Point{-1, 0}, Point{1280, 0}, Point{0, 720}, Point{std::numeric_limits<double>::quiet_NaN(), 0}}) {
        bool rejected = false;
        try { g.to_native(invalid); } catch (const std::invalid_argument&) { rejected = true; }
        assert(rejected);
    }
    bool rejected = false;
    try { absolute_input(1920, -2560, 4480); } catch (const std::invalid_argument&) { rejected = true; }
    assert(rejected);
}
