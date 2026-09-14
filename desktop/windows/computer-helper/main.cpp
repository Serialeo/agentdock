// P0 验证程序：没有 RPC、自动启动或任意目标输入入口。
#include <windows.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wincodec.h>
#include <wrl/client.h>
#include <shlobj.h>
#include <atomic>
#include <chrono>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <sstream>
#include <thread>
#include <vector>
#include <array>
#include "geometry.hpp"

using Microsoft::WRL::ComPtr;
using namespace computer_spike;
namespace fs = std::filesystem;
constexpr UINT WM_PROBE_DONE = WM_APP + 1;
constexpr int RUN_ID = 101, STOP_ID = 102;
constexpr ULONG_PTR INJECTION_TAG = 0x41444350;
std::atomic<bool> stopped{false}, running{false};
std::atomic<unsigned> hits{0};
std::thread worker;
RECT target_rect{80, 150, 360, 310};
std::wstring message = L"Move this window onto the display to test, then click Run once.";
fs::path last_report;
std::wstring pending_message;

std::string utf8(const std::wstring& value) {
    if (value.empty()) return {};
    int n = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    std::string out(n, '\0');
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), out.data(), n, nullptr, nullptr);
    return out;
}
std::string quoted(const std::string& value) {
    std::ostringstream out; out << '"';
    for (unsigned char c : value) {
        if (c == '"' || c == '\\') out << '\\' << c;
        else if (c < 32) out << "\\u" << std::hex << std::setw(4) << std::setfill('0') << static_cast<unsigned>(c);
        else out << c;
    }
    return out.str() + '"';
}
void check(HRESULT hr, const char* operation) {
    if (FAILED(hr)) { std::ostringstream out; out << operation << ": HRESULT 0x" << std::hex << static_cast<unsigned long>(hr); throw std::runtime_error(out.str()); }
}
void require(bool condition, const char* reason) { if (!condition) throw std::runtime_error(reason); }
long long ticks() { LARGE_INTEGER value{}; QueryPerformanceCounter(&value); return value.QuadPart; }
std::string object_name(HANDLE handle) {
    DWORD size = 0; GetUserObjectInformationW(handle, UOI_NAME, nullptr, 0, &size);
    std::vector<wchar_t> name(size / sizeof(wchar_t) + 1);
    if (!GetUserObjectInformationW(handle, UOI_NAME, name.data(), size, &size)) throw std::runtime_error("GetUserObjectInformation failed");
    return utf8(name.data());
}
void require_desktop() {
    DWORD session = 0;
    require(ProcessIdToSessionId(GetCurrentProcessId(), &session) && session != 0, "non-interactive process session");
    HDESK input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    require(input != nullptr, "input desktop unavailable (possibly locked or secure desktop)");
    std::string active;
    try { active = object_name(input); } catch (...) { CloseDesktop(input); throw; }
    CloseDesktop(input);
    require(active == object_name(GetThreadDesktop(GetCurrentThreadId())) && active == "Default", "not on the current Default input desktop");
}
struct Frame {
    int width{}, height{};
    long long present_time{};
    std::vector<unsigned char> bgra;
};
struct Capture {
    ComPtr<ID3D11Device> device;
    ComPtr<ID3D11DeviceContext> context;
    ComPtr<IDXGIOutputDuplication> duplication;
    DXGI_OUTPUT_DESC output{};
    explicit Capture(HMONITOR monitor) {
        ComPtr<IDXGIFactory1> factory;
        check(CreateDXGIFactory1(IID_PPV_ARGS(&factory)), "CreateDXGIFactory1");
        for (UINT a = 0;; ++a) {
            ComPtr<IDXGIAdapter1> adapter;
            HRESULT hr = factory->EnumAdapters1(a, &adapter);
            if (hr == DXGI_ERROR_NOT_FOUND) break;
            check(hr, "EnumAdapters1");
            for (UINT o = 0;; ++o) {
                ComPtr<IDXGIOutput> candidate;
                hr = adapter->EnumOutputs(o, &candidate);
                if (hr == DXGI_ERROR_NOT_FOUND) break;
                check(hr, "EnumOutputs");
                DXGI_OUTPUT_DESC desc{}; check(candidate->GetDesc(&desc), "GetDesc");
                if (desc.Monitor != monitor || !desc.AttachedToDesktop) continue;
                // P0 明确拒绝旋转显示器；未经真机校准不猜测 DXGI 旋转方向。
                require(desc.Rotation == DXGI_MODE_ROTATION_IDENTITY, "P0 rotation unsupported; record this display as not tested");
                output = desc;
                check(D3D11CreateDevice(adapter.Get(), D3D_DRIVER_TYPE_UNKNOWN, nullptr, D3D11_CREATE_DEVICE_BGRA_SUPPORT,
                    nullptr, 0, D3D11_SDK_VERSION, &device, nullptr, &context), "D3D11CreateDevice");
                ComPtr<IDXGIOutput1> output1; check(candidate.As(&output1), "IDXGIOutput1");
                check(output1->DuplicateOutput(device.Get(), &duplication), "DuplicateOutput");
                return;
            }
        }
        throw std::runtime_error("display not found in DXGI outputs");
    }
    Frame next(long long newer_than, std::chrono::steady_clock::time_point deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5)) {
        while (std::chrono::steady_clock::now() < deadline) {
            require(!stopped.load(), "cancelled");
            require_desktop();
            DXGI_OUTDUPL_FRAME_INFO info{}; ComPtr<IDXGIResource> resource;
            HRESULT hr = duplication->AcquireNextFrame(50, &info, &resource);
            if (hr == DXGI_ERROR_WAIT_TIMEOUT) continue;
            check(hr, "AcquireNextFrame (access loss requires a new Run; input is never replayed)");
            try {
                // 鼠标指针单独更新不算动作后的新桌面画面。
                if (info.LastPresentTime.QuadPart <= newer_than) { check(duplication->ReleaseFrame(), "ReleaseFrame"); continue; }
                ComPtr<ID3D11Texture2D> texture; check(resource.As(&texture), "frame texture");
                D3D11_TEXTURE2D_DESC desc{}; texture->GetDesc(&desc);
                require(desc.Width > 0 && desc.Height > 0 && static_cast<unsigned long long>(desc.Width) * desc.Height <= 40000000,
                    "capture exceeds P0 pixel limit");
                require(desc.Format == DXGI_FORMAT_B8G8R8A8_UNORM, "unexpected DXGI pixel format");
                desc.Usage = D3D11_USAGE_STAGING; desc.BindFlags = 0; desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ; desc.MiscFlags = 0;
                ComPtr<ID3D11Texture2D> staging; check(device->CreateTexture2D(&desc, nullptr, &staging), "staging texture");
                context->CopyResource(staging.Get(), texture.Get());
                Frame result{static_cast<int>(desc.Width), static_cast<int>(desc.Height), info.LastPresentTime.QuadPart, {}};
                result.bgra.resize(static_cast<size_t>(result.width) * result.height * 4);
                D3D11_MAPPED_SUBRESOURCE mapped{}; check(context->Map(staging.Get(), 0, D3D11_MAP_READ, 0, &mapped), "Map");
                for (int y = 0; y < result.height; ++y)
                    memcpy(result.bgra.data() + static_cast<size_t>(y) * result.width * 4,
                        static_cast<const unsigned char*>(mapped.pData) + static_cast<size_t>(y) * mapped.RowPitch,
                        static_cast<size_t>(result.width) * 4);
                context->Unmap(staging.Get(), 0);
                check(duplication->ReleaseFrame(), "ReleaseFrame");
                return result;
            } catch (...) { duplication->ReleaseFrame(); throw; }
        }
        throw std::runtime_error("no fresh desktop frame before deadline");
    }
};
void save_png(const fs::path& path, const Frame& frame) {
    ComPtr<IWICImagingFactory> factory;
    check(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&factory)), "WIC factory");
    ComPtr<IWICStream> stream; check(factory->CreateStream(&stream), "WIC stream");
    check(stream->InitializeFromFilename(path.c_str(), GENERIC_WRITE), "open PNG");
    ComPtr<IWICBitmapEncoder> encoder; check(factory->CreateEncoder(GUID_ContainerFormatPng, nullptr, &encoder), "PNG encoder");
    check(encoder->Initialize(stream.Get(), WICBitmapEncoderNoCache), "PNG initialize");
    ComPtr<IWICBitmapFrameEncode> output; check(encoder->CreateNewFrame(&output, nullptr), "PNG frame");
    check(output->Initialize(nullptr), "frame initialize");
    check(output->SetSize(frame.width, frame.height), "PNG size");
    WICPixelFormatGUID format = GUID_WICPixelFormat32bppBGRA;
    check(output->SetPixelFormat(&format), "PNG pixel format");
    require(IsEqualGUID(format, GUID_WICPixelFormat32bppBGRA), "WIC cannot encode BGRA");
    check(output->WritePixels(frame.height, frame.width * 4, static_cast<UINT>(frame.bgra.size()), const_cast<BYTE*>(frame.bgra.data())), "PNG write");
    check(output->Commit(), "PNG frame commit"); check(encoder->Commit(), "PNG commit");
}
std::array<int,3> rgb(const Frame& frame, Point p) {
    int x = static_cast<int>(std::lround(p.x)), y = static_cast<int>(std::lround(p.y));
    require(x >= 0 && y >= 0 && x < frame.width && y < frame.height, "sample outside frame");
    size_t i = (static_cast<size_t>(y) * frame.width + x) * 4;
    return {frame.bgra[i+2], frame.bgra[i+1], frame.bgra[i]};
}
fs::path report_directory() {
    PWSTR local = nullptr; check(SHGetKnownFolderPath(FOLDERID_LocalAppData, 0, nullptr, &local), "LocalAppData");
    fs::path root(local); CoTaskMemFree(local);
    GUID id{}; check(CoCreateGuid(&id), "report ID"); wchar_t text[40]{}; StringFromGUID2(id, text, 40);
    fs::path path = root / L"AgentDock" / L"computer-spike" / text;
    fs::create_directories(path); return path;
}
void probe(HWND window, RECT expected_target, unsigned initial_hits) {
    HRESULT com = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    std::ostringstream report;
    bool passed = false; unsigned inserted = 0; bool attempted = false;
    std::string error;
    try {
        check(com, "COM initialize");
        last_report = report_directory();
        wchar_t executable[32768]{}; GetModuleFileNameW(nullptr, executable, 32768);
        DWORD session = 0; ProcessIdToSessionId(GetCurrentProcessId(), &session);
        require_desktop();
        HANDLE token{}; require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token), "OpenProcessToken failed");
        TOKEN_ELEVATION elevation{}; DWORD length = 0;
        bool queried = GetTokenInformation(token, TokenElevation, &elevation, sizeof(elevation), &length) != 0;
        CloseHandle(token); require(queried, "GetTokenInformation failed");
        report << "\"platform\":\"windows\",\"executable\":" << quoted(utf8(executable))
            << ",\"os_session_id\":" << session << ",\"elevated\":" << (elevation.TokenIsElevated ? "true" : "false")
            << ",\"window_station\":" << quoted(object_name(GetProcessWindowStation()))
            << ",\"desktop\":" << quoted(object_name(GetThreadDesktop(GetCurrentThreadId())));
        require(AreDpiAwarenessContextsEqual(GetThreadDpiAwarenessContext(), DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2), "PerMonitorV2 not active");
        POINT center{(expected_target.left+expected_target.right)/2, (expected_target.top+expected_target.bottom)/2};
        HMONITOR monitor = MonitorFromPoint(center, MONITOR_DEFAULTTONULL); require(monitor != nullptr, "target has no display");
        for(int i=0;i<30;++i){require(!stopped.load(), "cancelled before input");std::this_thread::sleep_for(std::chrono::milliseconds(100));}
        Capture capture(monitor);
        RECT bounds = capture.output.DesktopCoordinates;
        require(expected_target.left >= bounds.left && expected_target.right <= bounds.right && expected_target.top >= bounds.top && expected_target.bottom <= bounds.bottom, "move full target onto one display");
        // Force an actual repaint before the initial capture; no input has been sent yet.
        InvalidateRect(window, nullptr, FALSE);
        Frame before = capture.next(0); save_png(last_report / L"before.png", before);
        Geometry geometry{before.width,before.height,{double(bounds.left),double(bounds.top),double(bounds.right-bounds.left),double(bounds.bottom-bounds.top)}};
        Point image_point = geometry.to_image({double(center.x),double(center.y)});
        Point native = geometry.to_native(image_point);
        Point sample = geometry.to_image({double(expected_target.left+30),double(expected_target.top+30)});
        auto old_rgb = rgb(before,sample);
        require(old_rgb[1]>old_rgb[0]+50 && old_rgb[1]>old_rgb[2]+50, "initial target is not green; check capture mapping or occlusion");
        report << ",\"image\":{\"width\":" << before.width << ",\"height\":" << before.height << "}"
            << ",\"desktop_bounds\":[" << bounds.left << ',' << bounds.top << ',' << bounds.right << ',' << bounds.bottom << ']'
            << ",\"image_point\":[" << image_point.x << ',' << image_point.y << ']'
            << ",\"native_point\":[" << native.x << ',' << native.y << ']'
            << ",\"before_present_qpc\":" << before.present_time;
        require(!stopped.load(), "cancelled before input"); require_desktop();
        POINT current[2]{{target_rect.left,target_rect.top},{target_rect.right,target_rect.bottom}};
        MapWindowPoints(window,nullptr,current,2);
        require(current[0].x==expected_target.left && current[0].y==expected_target.top && current[1].x==expected_target.right && current[1].y==expected_target.bottom, "window moved after observation");
        require(GetForegroundWindow()==window && GetAncestor(WindowFromPoint(center),GA_ROOT)==window, "calibration window lost focus or is occluded");
        require(!(GetAsyncKeyState(VK_LBUTTON)&0x8000), "physical mouse button still held");
        INPUT events[3]{};
        for(auto& event:events){event.type=INPUT_MOUSE;event.mi.dwExtraInfo=INJECTION_TAG;}
        events[0].mi.dwFlags=MOUSEEVENTF_MOVE|MOUSEEVENTF_ABSOLUTE|MOUSEEVENTF_VIRTUALDESK;
        events[0].mi.dx=absolute_input(native.x,GetSystemMetrics(SM_XVIRTUALSCREEN),GetSystemMetrics(SM_CXVIRTUALSCREEN));
        events[0].mi.dy=absolute_input(native.y,GetSystemMetrics(SM_YVIRTUALSCREEN),GetSystemMetrics(SM_CYVIRTUALSCREEN));
        events[1].mi.dwFlags=MOUSEEVENTF_LEFTDOWN; events[2].mi.dwFlags=MOUSEEVENTF_LEFTUP;
        auto input_started=ticks();
        report << ",\"input_started_qpc\":" << input_started;
        attempted=true; inserted=SendInput(3,events,sizeof(INPUT));
        // 部分插入也只清理 release；不重发移动或点击。
        if(inserted>0 && inserted<3){INPUT release{};release.type=INPUT_MOUSE;release.mi.dwFlags=MOUSEEVENTF_LEFTUP;release.mi.dwExtraInfo=INJECTION_TAG;SendInput(1,&release,sizeof(INPUT));}
        auto input_finished=ticks();
        require(inserted==3,"SendInput rejected some events; cause may include UIPI and is not proven");
        auto until=std::chrono::steady_clock::now()+std::chrono::seconds(3);
        while(hits.load()==initial_hits && std::chrono::steady_clock::now()<until){require(!stopped.load(),"cancelled after input");std::this_thread::sleep_for(std::chrono::milliseconds(10));}
        require(hits.load()==initial_hits+1,"expected injected click was not observed by calibration target");
        auto frame_deadline=std::chrono::steady_clock::now()+std::chrono::seconds(5);
        auto threshold=input_started;
        Frame after;
        std::array<int,3> new_rgb{};
        bool green=old_rgb[1]>old_rgb[0]+50 && old_rgb[1]>old_rgb[2]+50, blue=false;
        do {
            after=capture.next(threshold,frame_deadline);
            require(after.width==before.width && after.height==before.height,"display geometry changed");
            new_rgb=rgb(after,sample);
            blue=new_rgb[2]>new_rgb[0]+50 && new_rgb[2]>new_rgb[1]+30;
            threshold=after.present_time;
        } while(!blue);
        save_png(last_report/L"after.png",after);
        report << ",\"input_finished_qpc\":" << input_finished << ",\"after_present_qpc\":" << after.present_time
            << ",\"before_rgb\":[" << old_rgb[0] << ',' << old_rgb[1] << ',' << old_rgb[2] << ']'
            << ",\"after_rgb\":[" << new_rgb[0] << ',' << new_rgb[1] << ',' << new_rgb[2] << ']'
            << ",\"target_event_observed\":true,\"target_color_changed\":" << (green&&blue?"true":"false");
        require(green && blue,"captured target colors do not match calibration (check display color/HDR/occlusion)");
        passed=true;
    } catch(const std::exception& e){error=e.what();}
    try {
        if(last_report.empty()) last_report=report_directory();
        std::ofstream file(last_report/L"report.json");
        require(file.good(),"cannot create report.json");
        file << "{\"schema_version\":1,\"probe\":\"self_target_click\",\"passed\":" << (passed?"true":"false")
            << ",\"input_attempted\":" << (attempted?"true":"false") << ",\"accepted_events\":" << inserted
            << ",\"error\":" << quoted(error);
        if(!report.str().empty()) file << ',' << report.str();
        file << "}\n"; file.close(); require(!file.fail(),"report write failed");
        pending_message=(passed?L"PASS":L"NOT PASSED")+std::wstring(L" - Report: ")+last_report.wstring();
        if(!error.empty()) pending_message+=L"\n"+std::wstring(error.begin(),error.end());
    }catch(const std::exception&){pending_message=L"Report could not be saved. Do not treat this run as passed.";}
    if(SUCCEEDED(com)) CoUninitialize();
    PostMessageW(window,WM_PROBE_DONE,0,0);
}
LRESULT CALLBACK window_proc(HWND window,UINT msg,WPARAM w,LPARAM l) {
    switch(msg){
    case WM_CREATE:
        CreateWindowW(L"BUTTON",L"Run once",WS_CHILD|WS_VISIBLE,20,20,130,36,window,reinterpret_cast<HMENU>(RUN_ID),nullptr,nullptr);
        CreateWindowW(L"BUTTON",L"Stop",WS_CHILD|WS_VISIBLE,170,20,100,36,window,reinterpret_cast<HMENU>(STOP_ID),nullptr,nullptr);return 0;
    case WM_COMMAND:
        if(LOWORD(w)==STOP_ID){stopped.store(true);return 0;}
        if(LOWORD(w)==RUN_ID && !running.load()){
            if(worker.joinable())worker.join(); stopped.store(false); hits.store(0); last_report.clear();
            message=L"Preparing for 3 seconds (Stop cancels), then capture -> one click on green target -> fresh capture.";
            InvalidateRect(window,nullptr,FALSE); UpdateWindow(window);
            POINT corners[2]{{target_rect.left,target_rect.top},{target_rect.right,target_rect.bottom}};MapWindowPoints(window,nullptr,corners,2);
            RECT expected{corners[0].x,corners[0].y,corners[1].x,corners[1].y};
            running.store(true);worker=std::thread(probe,window,expected,0);return 0;
        }break;
    case WM_LBUTTONUP:{
        POINT point{static_cast<short>(LOWORD(l)),static_cast<short>(HIWORD(l))};
        if(running.load() && PtInRect(&target_rect,point) && static_cast<ULONG_PTR>(GetMessageExtraInfo())==INJECTION_TAG){hits.fetch_add(1);InvalidateRect(window,&target_rect,FALSE);UpdateWindow(window);}return 0;}
    case WM_PAINT:{
        PAINTSTRUCT paint{};HDC dc=BeginPaint(window,&paint);RECT area{};GetClientRect(window,&area);FillRect(dc,&area,reinterpret_cast<HBRUSH>(COLOR_WINDOW+1));
        SetBkMode(dc,TRANSPARENT);RECT text{20,65,740,140};DrawTextW(dc,message.c_str(),-1,&text,DT_LEFT|DT_WORDBREAK);
        HBRUSH color=CreateSolidBrush(hits.load()?RGB(20,60,210):RGB(20,180,60));FillRect(dc,&target_rect,color);DeleteObject(color);
        EndPaint(window,&paint);return 0;}
    case WM_PROBE_DONE:if(worker.joinable())worker.join();message=pending_message;running.store(false);InvalidateRect(window,nullptr,FALSE);return 0;
    case WM_DPICHANGED:{auto rect=reinterpret_cast<RECT*>(l);SetWindowPos(window,nullptr,rect->left,rect->top,rect->right-rect->left,rect->bottom-rect->top,SWP_NOZORDER|SWP_NOACTIVATE);return 0;}
    case WM_CLOSE:stopped.store(true);if(worker.joinable())worker.join();DestroyWindow(window);return 0;
    case WM_DESTROY:PostQuitMessage(0);return 0;
    }
    return DefWindowProcW(window,msg,w,l);
}
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,PWSTR,int show) {
    WNDCLASSW cls{};cls.lpfnWndProc=window_proc;cls.hInstance=instance;cls.lpszClassName=L"AgentDockComputerSpike";cls.hCursor=LoadCursor(nullptr,IDC_ARROW);
    if(!RegisterClassW(&cls))return 1;
    HWND window=CreateWindowExW(0,cls.lpszClassName,L"AgentDock Computer P0 - own calibration target only",WS_OVERLAPPEDWINDOW,CW_USEDEFAULT,CW_USEDEFAULT,800,420,nullptr,nullptr,instance,nullptr);
    if(!window)return 1;ShowWindow(window,show);
    MSG msg{};while(GetMessageW(&msg,nullptr,0,0)>0){TranslateMessage(&msg);DispatchMessageW(&msg);}return 0;
}
