//go:build darwin && cgo
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreImage/CoreImage.h>
#import <CoreMedia/CoreMedia.h>
#import "native_darwin.h"
#include <stdatomic.h>
void *ad_cancellation(void){return calloc(1,sizeof(_Atomic int));}
void ad_cancel(void *flag){atomic_store((_Atomic int*)flag,1);}

static BOOL desktopReady(void) {
 NSDictionary *session=CFBridgingRelease(CGSessionCopyCurrentDictionary());
 return session && [session[(__bridge NSString *)kCGSessionOnConsoleKey] boolValue]
   && ![session[@"CGSSessionScreenIsLocked"] boolValue];
}
int ad_status(int *observe,int *control) { @autoreleasepool {
 *observe=CGPreflightScreenCaptureAccess();*control=AXIsProcessTrusted();return desktopReady();
}}
void ad_permissions(void) { @autoreleasepool {
 CGRequestScreenCaptureAccess();
 AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{(__bridge NSString*)kAXTrustedCheckOptionPrompt:@YES});
}}
int ad_displays(uint32_t *ids,int capacity) { uint32_t count=0; if(CGGetActiveDisplayList(capacity,ids,&count)!=kCGErrorSuccess)return 0;return count; }
void ad_display(uint32_t id,int *width,int *height,int *rotation) { *width=(int)CGDisplayPixelsWide(id);*height=(int)CGDisplayPixelsHigh(id);*rotation=(int)CGDisplayRotation(id); }
static AXUIElementRef focusedWindow(pid_t *pid) {
 AXUIElementRef system=AXUIElementCreateSystemWide();CFTypeRef application=NULL,window=NULL;
 AXError err=AXUIElementCopyAttributeValue(system,kAXFocusedApplicationAttribute,&application);CFRelease(system);
 if(err!=kAXErrorSuccess||!application)return NULL;
 AXUIElementGetPid((AXUIElementRef)application,pid);
 AXUIElementCopyAttributeValue((AXUIElementRef)application,kAXFocusedWindowAttribute,&window);CFRelease(application);
 return (AXUIElementRef)window;
}
static BOOL targetState(ADShot *shot) {
 // CGWindowList 的前台窗口 ID/边界用于快照，AX 在输入前确认实际命中窗口。
 pid_t pid=0;AXUIElementRef focused=focusedWindow(&pid);
 if(focused)CFRelease(focused);
 // Observe-only users may not grant AX. Window metadata still comes from CG.
 NSArray *windows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));
 for(NSDictionary *window in windows){
  if([window[(__bridge NSString*)kCGWindowLayer] intValue]!=0)continue;
  if(pid && [window[(__bridge NSString*)kCGWindowOwnerPID] intValue]!=pid)continue;
  CGRect rect;
  if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(__bridge NSString*)kCGWindowBounds],&rect))continue;
  shot->target=[window[(__bridge NSString*)kCGWindowNumber] unsignedLongLongValue];
  shot->wx=rect.origin.x;shot->wy=rect.origin.y;shot->ww=rect.size.width;shot->wh=rect.size.height;return YES;
 }
 return NO;
}
@interface ADCapture : NSObject<SCStreamOutput>
@property(nonatomic,strong) NSData *png;
@property(nonatomic) int width;
@property(nonatomic) int height;
@property(nonatomic,strong) dispatch_semaphore_t ready;
@end
@implementation ADCapture
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)buffer ofType:(SCStreamOutputType)type {
 @autoreleasepool {
  if(type!=SCStreamOutputTypeScreen || !CMSampleBufferIsValid(buffer))return;
  NSArray *entries=(__bridge NSArray*)CMSampleBufferGetSampleAttachmentsArray(buffer,NO);
  NSDictionary *entry=entries.firstObject;
  if(!entry || [entry[SCStreamFrameInfoStatus] integerValue]!=SCFrameStatusComplete)return;
  CVImageBufferRef pixels=CMSampleBufferGetImageBuffer(buffer);if(!pixels)return;
  CIImage *ci=[CIImage imageWithCVPixelBuffer:pixels];
  CIContext *context=[CIContext contextWithOptions:nil];CGImageRef image=[context createCGImage:ci fromRect:ci.extent];if(!image)return;
  NSData *png=[[[NSBitmapImageRep alloc] initWithCGImage:image] representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
  @synchronized(self){if(!self.png){self.png=png;self.width=(int)CGImageGetWidth(image);self.height=(int)CGImageGetHeight(image);dispatch_semaphore_signal(self.ready);}}
  CGImageRelease(image);
 }
}
@end
int ad_capture(uint32_t display,int size,ADShot *shot) { @autoreleasepool {
 if(!desktopReady())return 1;if(!CGPreflightScreenCaptureAccess())return 2;
 if(!CGDisplayIsActive(display) || CGDisplayRotation(display)!=0)return 3;
 shot->display=display;CGRect bounds=CGDisplayBounds(display);shot->x=bounds.origin.x;shot->y=bounds.origin.y;shot->w=bounds.size.width;shot->h=bounds.size.height;
 if(!targetState(shot))return 3;
 __block SCShareableContent *content=nil;dispatch_semaphore_t ready=dispatch_semaphore_create(0);
 [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *value,NSError *error){content=value;dispatch_semaphore_signal(ready);}];
 if(dispatch_semaphore_wait(ready,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))!=0 || !content)return 4;
 SCDisplay *selected=nil;for(SCDisplay *candidate in content.displays){if(candidate.displayID==display){selected=candidate;break;}}if(!selected)return 3;
 SCContentFilter *filter=[[SCContentFilter alloc] initWithDisplay:selected excludingWindows:@[]];
 SCStreamConfiguration *config=[SCStreamConfiguration new];
 double ratio=fmin(1.0,(double)size/fmax(CGDisplayPixelsWide(display),CGDisplayPixelsHigh(display)));
 config.width=MAX(1,(size_t)(CGDisplayPixelsWide(display)*ratio));config.height=MAX(1,(size_t)(CGDisplayPixelsHigh(display)*ratio));
 config.showsCursor=YES;config.scalesToFit=YES;config.queueDepth=3;config.minimumFrameInterval=CMTimeMake(1,30);
 ADCapture *collector=[ADCapture new];collector.ready=dispatch_semaphore_create(0);
 SCStream *stream=[[SCStream alloc] initWithFilter:filter configuration:config delegate:nil];NSError *error=nil;
 if(![stream addStreamOutput:collector type:SCStreamOutputTypeScreen sampleHandlerQueue:dispatch_queue_create("com.uvwt.agentdock.computer.capture",DISPATCH_QUEUE_SERIAL) error:&error])return 4;
 [stream startCaptureWithCompletionHandler:^(NSError *failure){if(failure)dispatch_semaphore_signal(collector.ready);}];
 BOOL received=dispatch_semaphore_wait(collector.ready,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))==0;
 [stream stopCaptureWithCompletionHandler:^(NSError *failure){}];
 if(!received)return 4;
 @synchronized(collector){if(!collector.png)return 4;shot->length=(long)collector.png.length;shot->png=malloc(shot->length);if(!shot->png)return 4;memcpy(shot->png,collector.png.bytes,shot->length);shot->width=collector.width;shot->height=collector.height;}
 ADShot now={0};targetState(&now);
 if(!desktopReady()||now.target!=shot->target||now.wx!=shot->wx||now.wy!=shot->wy||now.ww!=shot->ww||now.wh!=shot->wh){free(shot->png);shot->png=NULL;return 3;}
 return 0;
}}
int ad_click(ADShot *shot,double x,double y,int button,void *cancelled) { @autoreleasepool {
 if(!desktopReady())return 1;if(!AXIsProcessTrusted())return 2;
 ADShot now={0};CGRect bounds=CGDisplayBounds(shot->display);
 if(!targetState(&now)||now.target!=shot->target||now.wx!=shot->wx||now.wy!=shot->wy||now.ww!=shot->ww||now.wh!=shot->wh||bounds.origin.x!=shot->x||bounds.origin.y!=shot->y||bounds.size.width!=shot->w||bounds.size.height!=shot->h)return 3;
 pid_t pid=0;AXUIElementRef focused=focusedWindow(&pid);if(!focused)return 3;
 AXUIElementRef system=AXUIElementCreateSystemWide(),hit=NULL;CFTypeRef window=NULL;
 AXUIElementCopyElementAtPosition(system,x,y,&hit);CFRelease(system);
 if(hit)AXUIElementCopyAttributeValue(hit,kAXWindowAttribute,&window);
 BOOL same=(window&&CFEqual(window,focused))||(hit&&CFEqual(hit,focused));
 if(window)CFRelease(window);if(hit)CFRelease(hit);CFRelease(focused);if(!same)return 3;
 for(int i=0;i<3;i++){if(CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState,(CGMouseButton)i))return 5;}
 CGMouseButton btn=button==1?kCGMouseButtonRight:(button==2?kCGMouseButtonCenter:kCGMouseButtonLeft);
 CGEventType down=button==1?kCGEventRightMouseDown:(button==2?kCGEventOtherMouseDown:kCGEventLeftMouseDown);
 CGEventType up=button==1?kCGEventRightMouseUp:(button==2?kCGEventOtherMouseUp:kCGEventLeftMouseUp);
 CGPoint point=CGPointMake(x,y);CGEventRef move=CGEventCreateMouseEvent(NULL,kCGEventMouseMoved,point,btn);
 CGEventRef press=CGEventCreateMouseEvent(NULL,down,point,btn),release=CGEventCreateMouseEvent(NULL,up,point,btn);
 if(!move||!press||!release){if(move)CFRelease(move);if(press)CFRelease(press);if(release)CFRelease(release);return 5;}
 CGEventSetIntegerValueField(press,kCGMouseEventClickState,1);CGEventSetIntegerValueField(release,kCGMouseEventClickState,1);
 if(atomic_load((_Atomic int*)cancelled)){CFRelease(move);CFRelease(press);CFRelease(release);return 6;}
 CGEventPost(kCGHIDEventTap,move);CGEventPost(kCGHIDEventTap,press);CGEventPost(kCGHIDEventTap,release);
 CFRelease(move);CFRelease(press);CFRelease(release);return 0;
}}

#include <pwd.h>
#include <unistd.h>
char *ad_user_home(void){struct passwd *account=getpwuid(getuid());return account&&account->pw_dir?strdup(account->pw_dir):NULL;}

void ad_run_loop(void){@autoreleasepool {
    NSApplication *application=[NSApplication sharedApplication];
    [application setActivationPolicy:NSApplicationActivationPolicyAccessory];
    [application run];
}}
void ad_stop_loop(void){dispatch_async(dispatch_get_main_queue(),^{
    [NSApp stop:nil];
    NSEvent *event=[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
    [NSApp postEvent:event atStart:NO];
});}
