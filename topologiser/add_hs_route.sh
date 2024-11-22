#!/bin/bash -x

DST=$1
VIA=$2

ip ro replace $VIA dev eth0
ip ro replace $DST via $VIA
