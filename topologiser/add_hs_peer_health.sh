#!/bin/bash -x

PEERID=$1
MAC=$2
BW=$3
DELAY=$4
JITTER=$5

GW=`cat /tmp/gw`


# See set_hs_peer_health.sh for details of all the TC voodoo going on here.

BURST=$((`cat /sys/class/net/eth0/mtu` + 14 + 1))

tc class add dev eth0 parent 1: classid 1:$PEERID drr
tc qdisc add dev eth0 parent 1:$PEERID handle ${PEERID}0: tbf rate ${BW}bit burst $BURST limit 10000
tc qdisc add dev eth0 parent ${PEERID}0:1 handle ${PEERID}1: netem delay ${DELAY}ms ${JITTER}ms 25%
tc filter add dev eth0 protocol ip parent 1: u32 match ether dst $MAC flowid 1:$PEERID
