#!/bin/bash

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

if [ "$#" -ne 3 ]
then
  echo 'Usage: ./start_hs.sh <NETWORK_ID> <HS_ID> <DOCKER_HOST_IP>'
  exit 1
fi

set -x

NETWORK_ID=$1
HSID=$2
HOST_IP=$3

docker run -d --name node$NETWORK_ID.$HSID \
	--privileged \
	--network mesh$NETWORK_ID \
	--hostname node$HSID \
	-p $((19000 + HSID + NETWORK_ID * 100)):3000 \
	gitlab.lip6.fr:5050/ie6/meshsim:latest
