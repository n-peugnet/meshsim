#!/bin/bash -x

GW=$(ip -o rout show default | cut -d' ' -f 3)
IP=$(ip -o addr show type inet4 scope global | cut -d' ' -f7)
NETWORK=${GW/%.1/.0}
PREFIX=${IP/*\/}

# if experimenting with tiny MTU:
#ip link set dev eth0 mtu 150

ip ro flush all
ip ro add $GW dev eth0
ip ro add blackhole $NETWORK/$PREFIX
ip ro add default via $GW
