// Used by scripts/linux-mount-check.sh (make test-linux-mounts) only: binds
// a folder under each item and makes its parent unreadable, in a loop.
// flip2 SECONDS SRC DIR1 DIR2 ... : loop { bind SRC on DIRi/d/m; chmod DIRi/d 000 } { chmod 755; umount }
#include <sys/mount.h>
#include <sys/stat.h>
#include <stdio.h>
#include <unistd.h>
#include <time.h>
#include <stdlib.h>
#include <string.h>
int main(int c, char **v){ int secs=atoi(v[1]); time_t end=time(0)+secs; long n=0; char d[4096], m[4096];
 while(time(0)<end && access("/tmp/flipstop",F_OK)!=0){
  for(int i=3;i<c;i++){ snprintf(m,sizeof m,"%s/d/m",v[i]); snprintf(d,sizeof d,"%s/d",v[i]); mount(v[2],m,NULL,MS_BIND,NULL); chmod(d,0); }
  usleep(200);
  for(int i=3;i<c;i++){ snprintf(m,sizeof m,"%s/d/m",v[i]); snprintf(d,sizeof d,"%s/d",v[i]); chmod(d,0755); umount2(m,0); }
  usleep(200);
  n++; }
 fprintf(stderr,"flips %ld\n",n); return 0;}
