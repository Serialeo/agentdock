#include <stdint.h>
typedef struct { int width,height; double x,y,w,h; uint32_t display; uint64_t target; double wx,wy,ww,wh; void *png; long length; } ADShot;
int ad_status(int *observe,int *control);
int ad_displays(uint32_t *ids,int capacity);
void ad_display(uint32_t id,int *width,int *height,int *rotation);
int ad_capture(uint32_t display,int size,ADShot *shot);
int ad_click(ADShot *shot,double x,double y,int button,void *cancelled);
void *ad_cancellation(void);
void ad_cancel(void *flag);
void ad_permissions(void);

char *ad_user_home(void);

void ad_run_loop(void);
void ad_stop_loop(void);
