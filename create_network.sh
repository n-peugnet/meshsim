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
  echo 'Usage: ./create_netork.sh <NETWORK_ID>'
  exit 1
fi

set -e

NETWORK_ID=$1

# Setup temp directory
tmpdir=/tmp/meshsim-mesh$NETWORK_ID
mkdir -p $tmpdir/prometheus-data
mkdir -p $tmpdir/prometheus-conf
cat <<EOF > $tmpdir/prometheus.yml
global:
  scrape_interval: ${PROMETHEUS_SCRAPE_INTERVAL:-10}s
scrape_configs:
  - job_name: 'file_discovery'
    file_sd_configs:
      - files:
          - /etc/prometheus/conf.d/*.json
EOF

# Create the network
docker network create --driver bridge mesh$NETWORK_ID > /dev/null

# Start one instance of prometheus
docker run -d --name prometheus$NETWORK_ID \
  --network=mesh$NETWORK_ID \
  --user=$(id -u):$(id -g) \
  -p 127.$NETWORK_ID.0.1:9090:9090 \
  -v /tmp/meshsim-mesh$NETWORK_ID/prometheus.yml:/etc/prometheus/prometheus.yml \
  -v /tmp/meshsim-mesh$NETWORK_ID/prometheus-conf:/etc/prometheus/conf.d \
  -v /tmp/meshsim-mesh$NETWORK_ID/prometheus-data:/prometheus \
  docker.io/prom/prometheus \
  > /dev/null

# Return the IP of the gateway
docker inspect mesh$NETWORK_ID -f '{{range.IPAM.Config}}{{.Gateway}}{{end}}' | head -n1
