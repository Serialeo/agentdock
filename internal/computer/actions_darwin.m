//go:build darwin && cgo && agentdock_computer_native

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import "native_darwin.h"

static uint64_t cgFlags(uint64_t flags){
 uint64_t out=0;if(flags&1)out|=kCGEventFlagMaskShift;if(flags&2)out|=kCGEventFlagMaskControl;if(flags&4)out|=kCGEventFlagMaskAlternate;if(flags&8)out|=kCGEventFlagMaskCommand;if(flags&16)out|=kCGEventFlagMaskSecondaryFn;return out;
}
int ad_pressed(uint16_t code){return CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState,code);}
int ad_send(ADInput *inputs,int count,int *sent){@autoreleasepool{
 *sent=0;
 for(int i=0;i<count;i++){
  ADInput e=inputs[i];CGEventRef event=NULL;
  CGMouseButton button=e.button==1?kCGMouseButtonRight:(e.button==2?kCGMouseButtonCenter:kCGMouseButtonLeft);
  if(e.kind<=4){
   CGEventType type=kCGEventMouseMoved;
   if(e.kind==2)type=e.button==1?kCGEventRightMouseDragged:(e.button==2?kCGEventOtherMouseDragged:kCGEventLeftMouseDragged);
   if(e.kind==3)type=e.button==1?kCGEventRightMouseDown:(e.button==2?kCGEventOtherMouseDown:kCGEventLeftMouseDown);
   if(e.kind==4)type=e.button==1?kCGEventRightMouseUp:(e.button==2?kCGEventOtherMouseUp:kCGEventLeftMouseUp);
   event=CGEventCreateMouseEvent(NULL,type,CGPointMake(e.x,e.y),button);
   if(event&&(e.kind==3||e.kind==4))CGEventSetIntegerValueField(event,kCGMouseEventClickState,e.count?e.count:1);
  }else if(e.kind==5){event=CGEventCreateScrollWheelEvent(NULL,kCGScrollEventUnitPixel,2,e.dy,-e.dx);}
  else if(e.kind==6||e.kind==7){event=CGEventCreateKeyboardEvent(NULL,e.code,e.kind==6);if(event)CGEventSetFlags(event,cgFlags(e.flags));}
  else if(e.kind==8){
   CGEventRef down=CGEventCreateKeyboardEvent(NULL,0,true),up=CGEventCreateKeyboardEvent(NULL,0,false);
   if(!down||!up){if(down)CFRelease(down);if(up)CFRelease(up);return 5;}
   CGEventSetFlags(down,0);CGEventSetFlags(up,0);
   CGEventKeyboardSetUnicodeString(down,e.length,e.text);CGEventKeyboardSetUnicodeString(up,e.length,e.text);
   CGEventPost(kCGHIDEventTap,down);CGEventPost(kCGHIDEventTap,up);CFRelease(down);CFRelease(up);(*sent)++;continue;
  }
  if(!event)return 5;CGEventPost(kCGHIDEventTap,event);CFRelease(event);(*sent)++;
 }
 return 0;
}}
