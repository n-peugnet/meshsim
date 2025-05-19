### Simulates a mesh network with Docker.

![meshsim](meshsim.gif)

Meshsim lets you define and manage an arbitrary network of nodes
in docker containers. It has two mode of operation:

 1. An interactive mode, where the nodes are instantiated by clicking
    on the canvas, and the network topology and latency may be adjusted by dragging
    nodes around.  Nodes connect to the nearest 5 nodes within the given latency
    threshold.

    The bandwidth and latency of individual network links can be overridden by clicking
    on the link (which will turn red) and adjusting the specific values.

 2. An automated mode, where the topology over time is provided by a directory
    of GraphML files.

The default image that meshsim launches is a minimal Debian with a prometheus node_exporter
and some network and debug tools pre-installed, but it can be configured to launch
a [custom image](#custom-image) (see also this image of
[Synapse for Meshsim](https://gitlab.lip6.fr/ie6/synapse-meshsim)).

It is also possible to [visualize traffic](#visualizing-traffic), provided that
the applications running in the containers are instrumented for it.

#### Notes

 * Uses Quart and NetworkX to model the network topology in python.
   * It puppets the dockerized nodes via `docker run` and talking HTTP to a `topologiser` daemon that runs on the container.
   * We deliberately use this rather than docker-compose or docker stack/swarm given the meshsim itself is acting as an orchestrator.
 * Uses D3 to visualise and control the network topology in browser.
 * Manually puppets the routing tables of the servers based on running dijkstra on the network topo
 * Manually puppets TC on the servers to cripple bandwidth, latency & jitter as desired.

Now usable in general.

#### Installation

 * Supported on macOS (with Docker-for-mac 18.06, not 2.0) & Linux

 * Using [pipx](https://pipx.pypa.io/stable/) (`sudo apt install pipx`)
   * Run: `pipx install git+https://gitlab.lip6.fr/ie6/meshsim.git`
   * Later, to upgrade: `pipx upgrade meshsim`

 * Install Docker (needs API 1.38; API 1.2x is known not to work.).
   * On Debian and derivatives: `sudo apt install docker.io`
   * Check the API with `docker version`.

#### Usage

 * run meshsim:  `meshsim <NETWORK_ID>` where `<NETWORK_ID>` is the ID between 0 and 9 to use for the docker network.
   Run `meshsim -h` for more options.
 * connect to meshsim at the indicated address.
 * click to create nodes
 * drag to move them around
 * => profit

You can log into the individual containers as `docker exec -it node$NETWORK.$N /bin/bash` to traceroute, ping
and generally see what see what's going on.
Inside a network, each node has the hostname `node$N`, which is automatically resolved.

#### Monitoring

Meshsim runs a Prometheus instance in each created network.
It can be accessed at `127.$NETWORK_ID.0.1:9090`, e.g. `http://127.1.0.1:9090` for network 1.

By default, it is configured to scrape metrics from the node_exporter of each node of the simulated network.
But it can be used to scrape other exporters, which can be useful when using [custom images](#custom-image).
It uses [file service discovery](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#file_sd_config)
to detect new configurations, so simply creating or modifying a JSON file in `/tmp/meshsim-mesh$NETWORK_ID/prometheus-conf`
will make Prometheus update its scrape configuration.

The `--metricsdump` option of Meshsim allows to dump the collected metrics in the specified file.
It will be in xz compressed textual OpenMetrics format.

#### Custom image

It is possible to build and run a custom image for Meshsim, the only requirement
is to include the `topologiser` inside. To do this, one can
take inspiration from the [root `Dockerfile`](Dockerfile), build the image with
a tag (e.g. `docker build --tag meshsim-custom .`), then run meshsim with the
`--start` option pointing to a custom start script (see [`start_hs.sh`](start_hs.sh)
for reference).

#### Visualizing traffic

Traffic on the network can be visualised in realtime by having nodes
emit telemetry to the simulator via websocket, showing which events are emitted
and received from which server via which network link.  Events are shown as
animated circles which follow the network links between servers.  When a node
processes an inbound event, it shows an animation of the event expanding and popping
like a bubble.

To support this visualization, apps must send logs to the main Meshsim process.
This has been made initially for the Matrix protocol, so the API is oriented
towards it:

    GET /log ? msg=SendingPDU & server=<NAME> & event_id=<ID> & destinations=<ARRAY>
    GET /log ? msg=ReceivedPDU & server=<NAME> & event_id=<ID> & origin=<NAME>

- `<NAME>` is the server's hostname
- `<ID>` is the event's unique ID
- `<ARRAY>` is a JSON array of servers' hostnames

All of these must of course be URL encoded.

#### Step-by-step debugging

To verify that everythig is working, you can realise the following checks in a Git checkout of meshsim:

 * create a docker network: `./create_network.sh 0`. This will return an IP that will be needed in the next steps.
 * check you can start a node via `./start_hs.sh 0 1 $DOCKER_IP` with 0 as networkid, 1 as hsid and DOCKER_IP being the IP returned in the previous step.
 * check if it's running with `docker stats`
 * log into the container to poke around with `docker exec -it node0.1 /bin/bash`
    * other nodes of the same network can be reached by their hostname, for instance `node1` for the current node.
 * shut it down nicely `./stop_clean_all.sh 0`

#### Limitations

Client-Server traffic shaping is not supported.

#### License

Copyright 2019 New Vector Ltd  
Copyright 2024-2025 Nicolas Peugnet <nicolas.peugnet@lip6.fr>

This file is part of meshsim.

meshsim is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

meshsim is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with meshsim.  If not, see <https://www.gnu.org/licenses/>.
