#!/bin/bash -x

GW=`cat /tmp/gw`

tc qdisc del dev eth0 root

# create a DRR classful qdisc at the root that will allow us to add
# classes later.
tc qdisc add dev eth0 root handle 1: drr

# create a new class and attach a queue that will be used as the default
# queue for talking to the host.
tc class add dev eth0 parent 1: classid 1:1 drr
tc qdisc add dev eth0 parent 1:1 handle 10: sfq

# filter default traffic to that queue.
tc filter add dev eth0 parent 1: matchall flowid 1:1
