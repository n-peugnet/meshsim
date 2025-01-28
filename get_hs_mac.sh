#!/bin/bash

NETWORK_ID=$1
HSID=$2

docker inspect -f '{{range .NetworkSettings.Networks}}{{.MacAddress}}{{end}}' node$NETWORK_ID.$HSID
