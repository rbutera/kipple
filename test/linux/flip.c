// Used by scripts/linux-mount-check.sh (make test-linux-mounts) only, in a
// throwaway privileged container: toggles bind mounts while kipple runs.
#include <sys/mount.h>
#include <stdio.h>
#include <unistd.h>
#include <time.h>
#include <stdlib.h>
// flip SECONDS SRC T1 T2 ... : toggle bind of SRC over each target as fast as possible
int main(int c, char **v){ int secs=atoi(v[1]); time_t end=time(0)+secs; long n=0;
 while(time(0)<end && access("/tmp/flipstop",F_OK)!=0){ for(int i=3;i<c;i++){ mount(v[2],v[i],NULL,MS_BIND,NULL);} for(int i=3;i<c;i++){ umount2(v[i],0);} n++; }
 fprintf(stderr,"flips %ld\n",n); return 0;}
