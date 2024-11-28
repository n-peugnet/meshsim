#!/bin/bash -x

PEERID=$1

# Create a virtual interface for each peer.
# This allows to greatly simplify the handling of TC rules, as this
# way, we can simply use a signle netem qdisc per peer, instead of
# relying on classful qdiscs.

# We use the bridge mode for no particular reason.

ip link add peer$PEERID link eth0 type macvlan mode bridge
ip link set peer$PEERID up

# Initialize the netem qdisc with a latency of 0ms, so that later,
# we just have to change it.
tc qdisc add dev peer$PEERID root netem delay 0ms
