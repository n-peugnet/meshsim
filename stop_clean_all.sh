#!/bin/bash

# Copyright 2019 New Vector Ltd
# Copyright 2025 Nicolas Peugnet <nicolas.peugnet@lip6.fr>
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

if [ "$#" -ne 1 ]
then
  echo 'Usage: ./stop_clean_all.sh <NETWORK_ID>'
  exit 1
fi

NETWORK_ID=$1

ids="$(docker container ls -f name=node$NETWORK_ID. -q -a) prometheus0"

echo "stopping $ids"
docker stop -t 0 $ids
docker rm $ids

docker network rm mesh$NETWORK_ID
rm -rf /tmp/meshsim-mesh$NETWORK_ID/
