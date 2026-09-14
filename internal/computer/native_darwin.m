//go:build darwin && cgo
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreImage/CoreImage.h>
#import <CoreMedia/CoreMedia.h>
#import "native_darwin.h"

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
 AXUIElementSetMessagingTimeout(system,0.25);
 AXError err=AXUIElementCopyAttributeValue(system,kAXFocusedApplicationAttribute,&application);CFRelease(system);
 if(err!=kAXErrorSuccess||!application)return NULL;
 AXUIElementSetMessagingTimeout((AXUIElementRef)application,0.25);
 AXUIElementGetPid((AXUIElementRef)application,pid);
 AXUIElementCopyAttributeValue((AXUIElementRef)application,kAXFocusedWindowAttribute,&window);CFRelease(application);
 return (AXUIElementRef)window;
}
static BOOL targetState(ADShot *shot) {
 // CGWindowList 的前台窗口 ID/边界用于快照，AX 在输入前确认实际命中窗口。
 pid_t pid=0;AXUIElementRef focused=focusedWindow(&pid);
 if(focused)CFRelease(focused);shot->pid=pid;
 // Observe-only users may not grant AX. Window metadata still comes from CG.
 NSArray *windows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));
 for(NSDictionary *window in windows){
  if([window[(__bridge NSString*)kCGWindowLayer] intValue]!=0)continue;
  if(pid && [window[(__bridge NSString*)kCGWindowOwnerPID] intValue]!=pid)continue;
  CGRect rect;
  if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(__bridge NSString*)kCGWindowBounds],&rect))continue;
  if(!shot->pid)shot->pid=[window[(__bridge NSString*)kCGWindowOwnerPID] intValue];
  shot->target=[window[(__bridge NSString*)kCGWindowNumber] unsignedLongLongValue];
  shot->wx=rect.origin.x;shot->wy=rect.origin.y;shot->ww=rect.size.width;shot->wh=rect.size.height;return YES;
 }
 return pid>0;
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
uint64_t ad_target(double x,double y,int32_t *owner) {@autoreleasepool{
 AXUIElementRef system=AXUIElementCreateSystemWide(),hit=NULL;
 AXUIElementSetMessagingTimeout(system,0.25);
 AXUIElementCopyElementAtPosition(system,x,y,&hit);CFRelease(system);
 *owner=0;if(hit){pid_t pid=0;AXUIElementGetPid(hit,&pid);*owner=pid;CFRelease(hit);}
 NSArray *windows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly,kCGNullWindowID));
 for(NSDictionary *window in windows){
  if(*owner && [window[(__bridge NSString*)kCGWindowOwnerPID] intValue]!=*owner)continue;
  if([window[(__bridge NSString*)kCGWindowAlpha] doubleValue]==0)continue;
  CGRect rect;if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(__bridge NSString*)kCGWindowBounds],&rect))continue;
  if(CGRectContainsPoint(rect,CGPointMake(x,y))){if(!*owner)*owner=[window[(__bridge NSString*)kCGWindowOwnerPID] intValue];return [window[(__bridge NSString*)kCGWindowNumber] unsignedLongLongValue];}
 }
 return 0;
}}
int ad_check(ADShot *shot,int continuing,int pointer,uint64_t target,int32_t targetPID,double x,double y) {@autoreleasepool{
 if(!desktopReady())return 1;if(!AXIsProcessTrusted())return 2;
 CGRect bounds=CGDisplayBounds(shot->display);
 if(!CGDisplayIsActive(shot->display)||bounds.origin.x!=shot->x||bounds.origin.y!=shot->y||bounds.size.width!=shot->w||bounds.size.height!=shot->h)return 3;
 ADShot now={0};if(!targetState(&now))return 3;
 if(!continuing){
  if(now.target!=shot->target||now.pid!=shot->pid||now.wx!=shot->wx||now.wy!=shot->wy||now.ww!=shot->ww||now.wh!=shot->wh)return 3;
  if(pointer&&target&&now.target==target)shot->inputFocused=1;
  if(pointer&&shot->bound){
   pid_t pid=0;AXUIElementRef focused=focusedWindow(&pid);if(!focused)return 3;
   AXUIElementRef system=AXUIElementCreateSystemWide(),hit=NULL;CFTypeRef window=NULL;
   AXUIElementSetMessagingTimeout(system,0.25);
 AXUIElementCopyElementAtPosition(system,x,y,&hit);CFRelease(system);
   if(hit)AXUIElementCopyAttributeValue(hit,kAXWindowAttribute,&window);
   BOOL same=(window&&CFEqual(window,focused))||(hit&&CFEqual(hit,focused));
   if(window)CFRelease(window);if(hit)CFRelease(hit);CFRelease(focused);if(!same)return 3;
  }
  for(int i=0;i<3;i++)if(CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState,(CGMouseButton)i))return 5;
 }else{
  if(pointer){
   if(now.pid!=shot->pid && now.pid!=targetPID)return 3;
   if(shot->bound && now.target!=shot->target)return 3;
   if(target){
    NSArray *windows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow,(CGWindowID)target));
    NSDictionary *window=windows.firstObject;if(!window)return 3;
    if([window[(__bridge NSString*)kCGWindowLayer] intValue]==0){
     if(now.target==target)shot->inputFocused=1;
     else if(shot->inputFocused||now.target!=shot->target)return 3;
    }
   }
  }
  else if(now.pid!=shot->pid||now.target!=shot->target)return 3;
 }
 return desktopReady()?0:1;
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
