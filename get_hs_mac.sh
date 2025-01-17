#!/bin/bash

NETWORK_ID=$1
HSID=$2

docker inspect -f '{{range .NetworkSettings.Networks}}{{.MacAddress}}{{end}}' synapse$NETWORK_ID.$HSID
