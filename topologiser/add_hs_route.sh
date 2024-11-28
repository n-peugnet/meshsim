#!/bin/bash -x

DST=$1
VIA=$2
ID=$3

ip ro replace $VIA dev peer$ID
ip ro replace $DST via $VIA
