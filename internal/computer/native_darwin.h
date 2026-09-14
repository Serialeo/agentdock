#include <stdint.h>
typedef struct { int width,height,bound,inputFocused; int32_t pid; double x,y,w,h; uint32_t display; uint64_t target; double wx,wy,ww,wh; void *png; long length; } ADShot;
int ad_status(int *observe,int *control);
int ad_displays(uint32_t *ids,int capacity);
void ad_display(uint32_t id,int *width,int *height,int *rotation);
int ad_capture(uint32_t display,int size,ADShot *shot);
typedef struct { int kind,button,count,dx,dy,length; double x,y; uint16_t code,text[2]; uint64_t flags; } ADInput;
int ad_send(ADInput *inputs,int count,int *sent);
int ad_pressed(uint16_t code);
uint64_t ad_target(double x,double y,int32_t *owner);
int ad_check(ADShot *shot,int continuing,int pointer,uint64_t target,int32_t targetPID,double x,double y);
void ad_permissions(void);

char *ad_user_home(void);

void ad_run_loop(void);
void ad_stop_loop(void);
