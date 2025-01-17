### Simulates a mesh of homeservers with Docker.

![meshsim](meshsim.gif)

Meshsim lets you define and manage an arbitrary network of Matrix homeservers
in docker containers via a web interface.  Servers are instantiated by clicking
on the canvas, and the network topology and latency may be adjusted by dragging
servers around.  Servers connect to the nearest 5 nodes within the given latency
threshold.

The bandwidth and latency of individual network links can be overridden by clicking
on the link (which will turn red) and adjusting the specific values.

Traffic on the network is visualised in realtime by having servers
emit telemetry to the simulator via websocket, showing which events are emitted
and received from which server via which network link.  Events are shown as
animated circles which follow the network links between servers.  When a server
processes an inbound event, it shows an animation of the event expanding and popping
like a bubble.

The default docker image that meshsim launches is https://github.com/matrix-org/meshsim-docker
which provides both a Synapse and a [coap-proxy](https://github.com/matrix-org/coap-proxy)
for low-bandwidth Matrix transport experimentation.

Further details can be found in our FOSDEM 2019 talk about meshsim and coap-proxy at:
https://matrix.org/blog/2019/03/12/breaking-the-100bps-barrier-with-matrix-meshsim-coap-proxy/

#### Notes

 * Requires a HS with a Dockerfile which lets it be run in a Debianish container to support KSM.
 * Uses KSM to share memory between the server containers.
 * Uses Flask and NetworkX to model the network topology in python.
   * It puppets the dockerized HSes via `docker run` and talking HTTP to a `topologiser` daemon that runs on the container.
   * We deliberately use this rather than docker-compose or docker stack/swarm given the meshsim itself is acting as an orchestrator.
 * Uses D3 to visualise and control the network topology in browser.
 * Manually puppets the routing tables of the servers based on running dijkstra on the network topo
 * Manually puppets TC on the servers to cripple bandwidth, latency & jitter as desired.

Now usable in general, but may be a bit fiddly to get up and running.

#### Installation

 * Supported on macOS (with Docker-for-mac 18.06, not 2.0) & Linux

 * Meshsim requires an up-to-date python installation of at least python 3.6
   * Check with `python --version` and/or `python3 --version`
   * Install python dependencies with `pip install -r requirements.txt` or `pip3 install -r requirements.txt`

 * Install Docker from docker.com (needs API 1.38; API 1.2x is known not to work.).
   * On Debian and derivatives: `sudo apt install docker.io`
   * Check the API with `docker version`.

 * Optional: Enable KSM on your host so your synapses can deduplicate RAM
   as much as possible

   ```sh
   screen ~/Library/Containers/com.docker.docker/Data/vms/0/tty  # on Docker-for-Mac
   echo 1 > /sys/kernel/mm/ksm/run
   echo 10000 > /sys/kernel/mm/ksm/pages_to_scan # 40MB of pages at a time

   # check to see if it's working (will only kick in once you start running something which requests KSM, like our KSMified synapse)
   grep -H '' /sys/kernel/mm/ksm/run/*
   ```

 * create a empty directory, e.g. `matrix-low-bandwidth`

 * check out meshsim
   ```
   matrix-low-bandwidth$ git clone https://github.com/n-peugnet/meshsim
   ```

 * Build the (KSM-capable) docker image:
   * Clone `synapse` repo and checkout the `n-peugnet/low-bandwidth` branch (inside the `matrix-low-bandwidth` directory)
     ```
     matrix-low-bandwidth$ git clone https://github.com/n-peugnet/synapse
     matrix-low-bandwidth$ cd synapse
     synapse$ git checkout n-peugnet/low-bandwidth
     ```

   * Clone the `meshsim-docker` repo (inside the `matrix-low-bandwidth` directory)
     ```
     matrix-low-bandwidth$ git clone https://github.com/n-peugnet/meshsim-docker
     ```

   * Clone the `coap-proxy` repo (inside the `matrix-low-bandwidth` directory)
     ```
     matrix-low-bandwidth$ git clone https://github.com/n-peugnet/coap-proxy
     ```

   * Run `docker build -t synapse -f meshsim-docker/Dockerfile .` from the top of the
     `matrix-low-bandwidth` directory (***not*** inside the `synapse` repo)

 * Optionally edit `start_hs.sh` to add bind mount to a local working copy of
   synapse. This allows doing synapse dev without having to rebuild images. See
   `start_hs.sh` for details. An example of the `docker run` command in `start_hs.sh` is below:

   ```
   docker run -d --name synapse$NETWORK_ID.$HSID \
   	--privileged \
   	--network mesh$NETWORK_ID \
   	--hostname synapse$HSID \
   	-e SYNAPSE_SERVER_NAME=synapse$HSID \
   	-e SYNAPSE_REPORT_STATS=no \
   	-e SYNAPSE_ENABLE_REGISTRATION=yes \
   	-e SYNAPSE_LOG_LEVEL=INFO \
   	-p $((18000 + HSID + NETWORK_ID * 100)):8008 \
   	-p $((19000 + HSID + NETWORK_ID * 100)):3000 \
   	-p $((20000 + HSID + NETWORK_ID * 100)):5683/udp \
   	-e SYNAPSE_LOG_HOST=$HOST_IP:$((3000 + NETWORK_ID * 100)) \
   	-e PROXY_DUMP_PAYLOADS=1 \
   	--mount type=bind,source=/home/user/matrix-low-bandwidth/coap-proxy,destination=/proxy \
   	--mount type=bind,source=/home/user/matrix-low-bandwidth/synapse/synapse,destination=/usr/local/lib/python3.7/site-packages/synapse \
   	synapse
   ```

#### Step-by-step checks

To verify that everythig is working, you can realise the following checks.

 * create a docker network: `docker network create --driver bridge mesh0`. Later
   we will need to know the gateway IP (so that the images can talk to
   meshsim, etc on the host). On MacOS `host.docker.internal` will work,
   otherwise run `docker network inspect mesh` and find the Gateway IP.
 * check you can start a synapse via `./start_hs.sh 0 1 $DOCKER_IP` with 0 as networkid, 1 as hsid and DOCKER_IP being the docker network gateway IP.
 * check if it's running with `docker stats`
 * check the supervisor logs with `docker logs -f synapse1`
 * log into the container to poke around with `docker exec -it synapse0.1 /bin/bash`
    * Actual synapse logs are located at `/var/log/supervisor/synapse*`

 * Check you can connect to its synapse at http://localhost:18001 (ports are 18000 + hsid + networkid*100).
   * Requires a Riot running on http on localhost or similar to support CORS to non-https
   * Initial user sign up may time out due to trying to connect to Riot-bot. Simply refresh the page and you should get in fine.
   * The KSM'd dockerfile autoprovisions an account on the HS called l/p matthew/secret for testing purposes.
 * Check that the topologiser is listening at http://localhost:19001 (ports are 19000 + hsid + networkid*100)
    * Don't expect to navigate to this URL and see anything more than a 404. As long as *something* is listening at this port, things are set up correctly.

 * shut it down nicely `./stop_clean_all.sh 0`

 * run meshsim:  `./meshsim.py <NETWORK_ID>` where `<NETWORK_ID>` is the ID between 0 and 9 to use for the docker network.
   Run `./meshsim.py -h` for more options.
 * connect to meshsim at the indicated address.
 * click to create HSes
 * drag to move them around
 * => profit

You can log into the individual synapse containers as `docker exec -it synapse$NETWORK.$N /bin/bash` to traceroute, ping
and generally see what see what's going on.

#### Using the CoAP proxy

* Build the proxy (see instruction in the [proxy's README](https://github.com/matrix-org/coap-proxy/blob/master/README.md))
* Run it by telling it to talk to the HS's proxy:

  ```bash
  ./bin/coap-proxy --coap-target localhost:20001 # Ports are 20000 + hsid
  ```

* Make clients talk to http://localhost:8888
* => profit

#### Limitations

Client-Server traffic shaping is only currently supported on macOS, as client->server traffic shaping
is currently implemented on the host (client) side.

#### License

Copyright 2019 New Vector Ltd

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
