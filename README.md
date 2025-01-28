### Simulates a mesh network with Docker.

![meshsim](meshsim.gif)

Meshsim lets you define and manage an arbitrary network of nodes
in docker containers via a web interface.  Nodes are instantiated by clicking
on the canvas, and the network topology and latency may be adjusted by dragging
nodes around.  Nodes connect to the nearest 5 nodes within the given latency
threshold.

The bandwidth and latency of individual network links can be overridden by clicking
on the link (which will turn red) and adjusting the specific values.

Traffic on the network is visualised in realtime by having nodes
emit telemetry to the simulator via websocket, showing which events are emitted
and received from which server via which network link.  Events are shown as
animated circles which follow the network links between servers.  When a node
processes an inbound event, it shows an animation of the event expanding and popping
like a bubble.

The default image that meshsim launches is a minimal Debian with some network and
debug tools pre-installed.

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

 * Meshsim requires an up-to-date python installation of at least python 3.6
   * Check with `python --version` and/or `python3 --version`
   * Install python dependencies with `pip install -r requirements.txt` or `pip3 install -r requirements.txt`

 * Install Docker (needs API 1.38; API 1.2x is known not to work.).
   * On Debian and derivatives: `sudo apt install docker.io`
   * Check the API with `docker version`.

#### Usage

 * run meshsim:  `./meshsim.py <NETWORK_ID>` where `<NETWORK_ID>` is the ID between 0 and 9 to use for the docker network.
   Run `./meshsim.py -h` for more options.
 * connect to meshsim at the indicated address.
 * click to create nodes
 * drag to move them around
 * => profit

You can log into the individual containers as `docker exec -it node$NETWORK.$N /bin/bash` to traceroute, ping
and generally see what see what's going on.

#### Custom image

It is possible to build and run a custom image for Meshsim, to do this, one can
take inspiration from the [root `Dockerfile`](Dockerfile), build the image with
a tag (e.g. `docker build --tag meshsim-custom .`), then edit the start_hs.sh
script to run the desired image based on its tag.

#### Step-by-step checks

To verify that everythig is working, you can realise the following checks.

 * create a docker network: `docker network create --driver bridge mesh0`. Later
   we will need to know the gateway IP (so that the images can talk to
   meshsim, etc on the host). On MacOS `host.docker.internal` will work,
   otherwise run `docker network inspect mesh` and find the Gateway IP.
 * check you can start a node via `./start_hs.sh 0 1 $DOCKER_IP` with 0 as networkid, 1 as hsid and DOCKER_IP being the docker network gateway IP.
 * check if it's running with `docker stats`
 * log into the container to poke around with `docker exec -it node0.1 /bin/bash`
    * other nodes of the same network can be reached by their hostname, for instance `node1` for the current node.
 * Check that the topologiser is listening at http://localhost:19001 (ports are 19000 + hsid + networkid*100)
    * Don't expect to navigate to this URL and see anything more than a 404. As long as *something* is listening at this port, things are set up correctly.

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
