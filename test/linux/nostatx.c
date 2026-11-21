// Used by scripts/linux-mount-check.sh (make test-linux-mounts) only: runs
// a command with statx denied, like kernels before 5.8, so kipple falls
// back to /proc/self/mountinfo.
// nostatx CMD ARGS... : run CMD with statx returning ENOSYS (seccomp), no_new_privs
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <linux/audit.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <stddef.h>
#include <errno.h>
#include <stdio.h>
#include <unistd.h>
int main(int c,char**v){
 struct sock_filter f[]={
  BPF_STMT(BPF_LD|BPF_W|BPF_ABS, offsetof(struct seccomp_data,nr)),
  BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K, __NR_statx, 0, 1),
  BPF_STMT(BPF_RET|BPF_K, SECCOMP_RET_ERRNO|(ENOSYS&SECCOMP_RET_DATA)),
  BPF_STMT(BPF_RET|BPF_K, SECCOMP_RET_ALLOW)};
 struct sock_fprog p={sizeof(f)/sizeof(f[0]),f};
 if(prctl(PR_SET_NO_NEW_PRIVS,1,0,0,0)||prctl(PR_SET_SECCOMP,SECCOMP_MODE_FILTER,&p)){perror("seccomp");return 1;}
 execvp(v[1],v+1); perror("exec"); return 1;}
