#!/bin/bash -x

# Copyright 2019 New Vector Ltd
#
# This file is part of meshsim.
#
# meshsim is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# meshsim is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with coap-proxy.  If not, see <https://www.gnu.org/licenses/>.

PEERID=$1
BW=$2
DELAY=$3
JITTER=${4}

#      root 1: drr
#            |
#      +-----+-----+---  ...  --+
#      |     |     |            |
#     1:1   1:2   1:3    ...   1:n
#      |     |     |            |
#     10:   20:   30:    ...   n0:
#     sfq   tbf   tbf          tbf
#            |     |            |
#           20:1  30:1   ...   n0:1
#            |     |            |
#           21:   31:    ...   n1:
#           delay delay        delay
#            |     |            |

# sample data:
# BW=300
# DELAY=100
# JITTER=10

# burst is the number of bytes that we're allowed to send rapidly before the rate limiting
# kicks in (which then compensates to even things out to hit the target rate).
# it has to be > rate limit/scheduler hz (which is easy, given we're on zero HZ sched).
# it also has to be bigger than MTU, otherwise we'll never gather enough tokens in our bucket to
# be able to send a packet.
#
# limit is the maximum size of packets (in bytes) which we allow to stack up before buffering.
# this is converted to latency (in ms) when viewed by `tc -s disc show`
# e.g. a limit of 3000 bytes == (rate/8 * latency) + burst.
# e.g. (256144/8 * 0.0437) + 1600 = 3000 in the example below:
#
# qdisc tbf 30: dev eth0 parent 1:3 rate 256144bit burst 1599b lat 43.7ms

# we deliberately set the burst rate to be as low as possible - i.e. the MTU (1500 bytes) to smooth
# the bitrate as much as possible. this may overly cripple fast transfers as the token bucket can't
# fill up fast enough but hopefully it'll be okay as our sched is zero hertz.
# It might result in way too many interrupts though.
#
# we deliberately pick MTU + 14 (MAC headers) + 1 to avoid rounding errors
BURST=$((`cat /sys/class/net/eth0/mtu` + 14 + 1))

# N.B. we have to pick a tiny MTU as at a 300bps link, 1514 bytes takes 40s to transfer.
# Ideall we would pick an MTU of 150 (so 14 bytes of MAC, 20 IP, 8 UDP + 106 bytes of payload.)
# otherwise we're going to always get 40s for free where everything works fine before suddenly the
# rate limiting kicks in.  It might be better to just have a better TC module...

tc qdisc change dev eth0 parent 1:$PEERID handle ${PEERID}0: tbf rate ${BW}bit burst $BURST limit 10000
tc qdisc change dev eth0 parent ${PEERID}0:1 handle ${PEERID}1: netem delay ${DELAY}ms ${JITTER}ms 25%

# to diagnose:
#
# tc qdisc show dev eth0
# tc -s qdisc show dev eth0
# tc filter show dev eth0

# or if we were doing it by IP:

#tc filter add dev eth0 protocol ip parent 1:0 prio 3 u32 match ip dst ${PEER}/32 flowid 1:3
