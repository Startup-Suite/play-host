#!/usr/bin/env bash
# moon-cpu-01a0dbd6.sh <seconds> <label>: every 3 s, moon's whole-host CPU
# busy (per mille of all 16 cpus, /proc/stat deltas) and the Chrome
# container's CPU in millicores (cgroup cpu.stat usage_usec deltas / wall time).
# Output: ~/sources/tmp/rig-01a0dbd6/s5/moon-cpu-<label>-01a0dbd6.log (on hive).
set -uo pipefail
secs=$1; label=$2
out=~/sources/tmp/rig-01a0dbd6/s5/moon-cpu-$label-01a0dbd6.log
ssh rocks@moon.local "cg=/sys/fs/cgroup\$(podman inspect --format '{{.State.CgroupPath}}' chrome-01a0dbd6-s5)/cpu.stat
end=\$((\$(date +%s)+$secs)); read -r _ a b c d e f g h _ < /proc/stat; pt=\$((a+b+c+d+e+f+g+h)); pi=\$((d+e));
pu=\$(awk '/usage_usec/{print \$2}' \$cg); pw=\$(date +%s%N)
while [ \$(date +%s) -lt \$end ]; do sleep 3; read -r _ a b c d e f g h _ < /proc/stat; t=\$((a+b+c+d+e+f+g+h)); i=\$((d+e));
busy=\$(( 1000*((t-pt)-(i-pi))/(t-pt) )); pt=\$t; pi=\$i;
u=\$(awk '/usage_usec/{print \$2}' \$cg); w=\$(date +%s%N); mcores=\$(( (u-pu)*1000/((w-pw)/1000) )); pu=\$u; pw=\$w;
echo \"\$(date -u +%H:%M:%S) host_busy_permille=\$busy chrome_millicores=\$mcores\"; done" > "$out" 2>&1
