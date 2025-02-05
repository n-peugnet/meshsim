#!/bin/bash

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
  echo 'Usage: ./dump_metrics.sh <NETWORK_ID>'
  exit 1
fi

set -e

NETWORK_ID=$1

# Start one instance of prometheus
docker run --rm \
  --user=$(id -u):$(id -g) \
  -v /tmp/meshsim-mesh$NETWORK_ID/prometheus-data:/prometheus \
  --entrypoint=/bin/promtool \
  docker.io/prom/prometheus \
  tsdb dump-openmetrics /prometheus | xz -T4
