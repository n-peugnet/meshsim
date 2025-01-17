#!/usr/bin/env python3

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

import argparse
import asyncio
from asyncio.subprocess import Process
import atexit
import json
import os
import subprocess
from contextlib import contextmanager
from itertools import combinations
from math import sqrt
import signal
import sys
import time

import aiohttp
import async_timeout
import networkx as nx
from hypercorn.asyncio import serve
from hypercorn.config import Config as HyperConfig
from quart import Quart, abort, jsonify, request, send_from_directory, websocket
from tenacity import retry, wait_fixed
from tqdm import tqdm

args = None
id = 0
host = ""
stdout = subprocess.DEVNULL
stderr = subprocess.DEVNULL

class App(Quart):
    def run_simple_task( self, host: str = "0.0.0.0", port: int = 3000, debug: bool | None = None):
        config = HyperConfig()
        config.access_log_format = "%(h)s %(r)s %(s)s %(b)s %(D)s"
        config.accesslog = "/dev/stderr"
        config.bind = [f"{host}:{port}"]
        if debug is not None:
            self.debug = debug
        config.errorlog = "/dev/stderr"
        return serve(self, config)

app = App(__name__)
event_notif_queue = asyncio.Queue()


async def put(url, data, timeout=1):
    async with aiohttp.ClientSession() as session, async_timeout.timeout(timeout):
        async with session.put(
            url, data=data, headers={"Content-type": "application/json"}
        ) as response:
            return await response.text()


class Server(object):
    _id = 0

    def __init__(self, x, y):
        self.x = x
        self.y = y
        self.id = Server._id
        self.ip = None
        self.mac = None
        Server._id = Server._id + 1

        self.paths = None  # cache of shortest paths
        self.path_costs = None  # cache of shortest path costs

        self.neighbours = set()

    def toDict(self):
        return {"id": self.id, "ip": self.ip, "mac": self.mac}

    async def start(self):
        global args
        proc = await asyncio.create_subprocess_exec(
            "./start_hs.sh", str(id), str(self.id), host,
            stdout=stdout, stderr=stderr,
        )
        code = await proc.wait()
        if code != 0:
            raise Exception("Failed to start HS")
        await self.update_network_info()
        await(asyncio.sleep(3))

    async def update_network_info(self):
        proc = await asyncio.create_subprocess_exec(
            "./get_hs_ip.sh", str(id), str(self.id), stdout=asyncio.subprocess.PIPE
        )
        stdout, _ = await proc.communicate()
        self.ip = stdout.decode().strip()

        proc = await asyncio.create_subprocess_exec(
            "./get_hs_mac.sh", str(id), str(self.id), stdout=asyncio.subprocess.PIPE
        )
        stdout, _ = await proc.communicate()
        self.mac = stdout.decode().strip()

    async def set_routes(self, routes):
        # [
        #   {
        #       dst: server,
        #       via: server
        #   }, ...
        # ]
        data = json.dumps(routes, indent=4)
        app.logger.info("setting routes for %d: %s", self.id, data)

        try:
            r = await put("http://localhost:%d/routes" % (19000 + self.id + id*100), data)

            app.logger.info("Set route with result for %d: %s", self.id, r)
        except asyncio.TimeoutError:
            app.logger.warning("Set route for %d: timeout", self.id)
        except Exception:
            app.logger.exception("Set route for %d: unexpected error:", self.id)

    async def set_network_health(self, health):
        # {
        #     peers: [
        #         {
        #             peer: <server>,
        #             bandwidth: 300, # 300bps
        #             latency: 200, # 200ms
        #             jitter: 20, # +/- 20ms - we apply 25% correlation on jitter
        #             loss: 0, # 0% packet loss
        #         }, ...
        #     ],
        # }
        data = json.dumps(health, indent=4)
        app.logger.info("setting health for %d: %s", self.id, data)

        try:
            r = await put("http://localhost:%d/health" % (19000 + self.id + id*100), data)

            app.logger.info("Set health with result for %d: %s", self.id, r)
        except asyncio.TimeoutError:
            app.logger.warning("Set health for %d: timeout", self.id)
        except Exception:
            app.logger.exception("Set health for %d: unexpected error:", self.id)

    def stop(self):
        subprocess.call(["./stop_hs.sh", str(self.id)])

    def distance(self, server2):
        return sqrt((server2.x - self.x) ** 2 + (server2.y - self.y) ** 2)

    def connect(self, server2, limit=None):
        if limit and (len(self.neighbours) > limit or len(server2.neighbours) > limit):
            return False

        # app.logger.info("connecting %d to %d", self.id, server2.id)

        self.neighbours.add(server2)
        server2.neighbours.add(self)
        return True

    def reset_neighbours(self):
        # app.logger.info("resetting neighbours for %d", self.id)
        self.neighbours = set()


class Mesh:

    COST_MIN_LATENCY = "cost_min_latency"
    COST_MAX_BANDWIDTH = "cost_max_bandwidth"

    def __init__(self, host_ip):
        self.graph = nx.Graph()
        self.servers = {}

        self.rewiring = False
        self.pending_rewire = False

        # global defaults
        self.bandwidth = 5120000
        self.decay_bandwidth = True
        self.latency_scale = 100
        self.max_latency = 100
        self.min_bandwidth = 0
        self.jitter = 0
        self.packet_loss = 0
        self.cost_function = Mesh.COST_MIN_LATENCY

        # link overrides
        self.overrides = {}

        # Number of things that are about to call rewire. Don't bother rewiring
        # unless this is zero.
        self._about_to_rewire_functions = 0

    async def add_server(self, server):
        # we deliberately add the server asap so we can echo its existence
        # back to the UI. however, we have to deliberately ignore it from
        # wiring calculations given it hasn't yet started
        self.servers[server.id] = server
        self.graph.add_node(server.id)

        with self.will_rewire():
            await server.start()

        await self.safe_auto_rewire()
        return server

    def get_server(self, server_id) -> Server:
        return self.servers[server_id]

    def get_started_servers(self) -> dict[int, Server]:
        return {
            i: self.servers[i] for i in self.servers if self.servers[i].ip is not None
        }

    async def move_server(self, server, x, y):
        server.x = x
        server.y = y
        await self.safe_auto_rewire()

    async def remove_server(self, server):
        server.stop()
        self.graph.remove_node(server.id)

        await self.safe_auto_rewire()

    async def safe_auto_rewire(self):
        if self._about_to_rewire_functions:
            app.logger.info("Skipping rewire as one will be triggered")
            return

        if self.rewiring:
            # no point in stacking them up
            if self.pending_rewire:
                app.logger.info(
                    "Skipping rewire as one already happening and one already queued"
                )
            else:
                app.logger.info("Deferring rewire as one already happening")
            self.pending_rewire = True
            return

        self.rewiring = True

        try:
            await self._auto_rewire()
        finally:
            self.rewiring = False
            if self.pending_rewire:
                self.pending_rewire = False
                await self.safe_auto_rewire()

    async def _auto_rewire(self):
        if self.cost_function == Mesh.COST_MIN_LATENCY:
            cost_function = self.get_latency
        elif self.cost_function == Mesh.COST_MAX_BANDWIDTH:
            cost_function = self.get_bandwidth_cost

        started_servers = self.get_started_servers()

        # reset neighbours from the server's point of view.
        # only for servers which have started up and have IPs
        for server in started_servers.values():
            # Uncomment if we want to recheck IP/mac addresses of the containers:
            # await server.update_network_info()

            server.reset_neighbours()

        # remove all edges from the underlying graph.
        self.graph.remove_edges_from(list(self.graph.edges()))

        # first we wire anyone closer together than our thresholds
        for server1, server2 in combinations(started_servers.values(), 2):
            latency = self.get_latency(server1, server2)
            bandwidth = self.get_bandwidth(server1, server2)

            if latency < self.max_latency and bandwidth > self.min_bandwidth:
                server1.connect(server2)

        # then we reset the wirings and rewire the closest 4 neighbours.
        # we do this in two phases as we need to have all the possible
        # neighbours in place from both 'i' and 'j' sides of the matrix before
        # we know which are actually closest.
        for server in started_servers.values():
            neighbour_costs = {
                s.id: cost_function(server, s) for s in server.neighbours
            }
            server.reset_neighbours()
            for (j, cost) in sorted(neighbour_costs.items(), key=lambda x: x[1])[0:4]:
                if server.connect(started_servers[j], 4):
                    self.graph.add_edge(server.id, j, weight=cost)

        self.paths = nx.shortest_path(self.graph, weight="weight")
        self.path_costs = dict(nx.shortest_path_length(self.graph, weight="weight"))

        # app.logger.info("calculated shortest paths as %r", self.paths)
        await self._do_rewire(started_servers, self.paths, self.path_costs)

    async def _do_rewire(self, started_servers, paths, path_costs):
        c = list(combinations(started_servers, 2))
        app.logger.info("combinations %r", c)

        futures = (
            # apply the network topology in terms of routing table
            [
                self.get_server(source_id).set_routes(
                    [
                        {
                            "dst": self.get_server(dest_id).toDict(),
                            "via": (
                                self.get_server(
                                    paths[source_id][dest_id][1]
                                ).toDict()
                                if len(paths[source_id].get(dest_id, [])) > 1
                                else None
                            ),
                            "cost": path_costs[source_id].get(dest_id),
                        }
                        for dest_id in started_servers
                        if source_id != dest_id
                    ]
                )
                for source_id in started_servers
            ]
            +
            # apply the network characteristics to the peers
            [
                self.get_server(i).set_network_health(
                    {
                        "peers": [
                            {
                                "peer": neighbour.toDict(),
                                "bandwidth": self.get_bandwidth(
                                    self.get_server(i), neighbour
                                ),
                                "latency": self.get_latency(
                                    self.get_server(i), neighbour
                                ),
                                "jitter": self.get_jitter(
                                    self.get_server(i), neighbour
                                ),
                                "packet_loss": self.get_packet_loss(
                                    self.get_server(i), neighbour
                                ),
                            }
                            for neighbour in self.get_server(i).neighbours
                        ],
                    }
                )
                for i in started_servers
            ]
        )

        await asyncio.gather(*futures)

    def get_bandwidth_cost(self, server1, server2):
        return 1 / self.get_bandwidth(server1, server2)

    def get_bandwidth(self, server1, server2):
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp

        override = self.overrides.get(server1.id, {}).get(server2.id, None)
        if override and override.get("bandwidth") is not None:
            return override.get("bandwidth")

        if self.decay_bandwidth:
            distance = server1.distance(server2)
            return int(
                self.bandwidth
                * ((self.max_latency - server1.distance(server2)) / self.max_latency)
            )
        else:
            return self.bandwidth

    def get_latency(self, server1, server2):
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp

        override = self.overrides.get(server1.id, {}).get(server2.id, None)
        if override and override.get("latency") is not None:
            return override["latency"] * (self.latency_scale / 100)

        return int(server1.distance(server2)) * (self.latency_scale / 100)

    def get_jitter(self, server1, server2):
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp

        override = self.overrides.get(server1.id, {}).get(server2.id, None)
        if override and override.get("jitter") is not None:
            return override["jitter"]

        return self.jitter

    def get_packet_loss(self, server1, server2):
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp

        override = self.overrides.get(server1.id, {}).get(server2.id, None)
        if override and override.get("packet_loss") is not None:
            return override["packet_loss"]

        return self.packet_loss

    def set_link_health(self, server1_id, server2_id, health):
        override = {}
        for t in ["bandwidth", "latency", "jitter", "packet_loss"]:
            if t in health:
                override[t] = int(health.get(t)) if health.get(t) is not None else None

        self.overrides.setdefault(server1_id, {}).setdefault(server2_id, {}).update(
            override
        )
        app.logger.info("link health overrides now %r", self.overrides)

    def get_d3_data(self):
        data = {"nodes": [], "links": []}

        # XXX: frontend relies on index in data.nodes matches the server ID
        for _, server in sorted(self.servers.items()):
            data["nodes"].append({"name": server.id, "x": server.x, "y": server.y})
            for neighbour in server.neighbours:
                if server.id < neighbour.id:
                    link = {
                        "source": server.id,
                        "target": neighbour.id,
                        "bandwidth": self.get_bandwidth(server, neighbour),
                        "latency": self.get_latency(server, neighbour),
                        "jitter": self.get_jitter(server, neighbour),
                        "packet_loss": self.get_packet_loss(server, neighbour),
                    }

                    overrides = self.overrides.get(server.id, {}).get(neighbour.id, {})
                    for override in overrides:
                        if overrides[override] is not None:
                            link.setdefault("overrides", {})[override] = True

                    data["links"].append(link)

        return json.dumps(data, sort_keys=True, indent=4)

    def get_costs(self):
        if not self.path_costs:
            self.path_costs = dict(nx.shortest_path_length(self.graph, weight="weight"))
        return self.paths_costs

    def get_path(self, origin, target):
        if not self.paths:
            self.paths = nx.shortest_path(self.graph, weight="weight")
        return self.paths[origin][target]

    def get_defaults(self):
        return {
            "bandwidth": self.bandwidth,
            "decay_bandwidth": self.decay_bandwidth,
            "max_latency": self.max_latency,
            "min_bandwidth": self.min_bandwidth,
            "jitter": self.jitter,
            "packet_loss": self.packet_loss,
            "cost_function": self.cost_function,
            "latency_scale": self.latency_scale,
        }

    def set_defaults(self, defaults):
        self.bandwidth = int(defaults.get("bandwidth", self.bandwidth))
        self.decay_bandwidth = bool(
            defaults.get("decay_bandwidth", self.decay_bandwidth)
        )
        self.max_latency = int(defaults.get("max_latency", self.max_latency))
        self.min_bandwidth = int(defaults.get("min_bandwidth", self.min_bandwidth))
        self.jitter = int(defaults.get("jitter", self.jitter))
        self.packet_loss = int(defaults.get("packet_loss", self.packet_loss))
        self.cost_function = defaults.get("cost_function", self.cost_function)
        self.latency_scale = int(defaults.get("latency_scale", self.jitter))

    @contextmanager
    def will_rewire(self):
        try:
            self._about_to_rewire_functions += 1
            yield
        finally:
            self._about_to_rewire_functions -= 1


class DynMesh(Mesh):

    def __init__(self, host_ip):
        super().__init__(host_ip)
        self.input_id2id = dict()

    async def safe_auto_rewire(self):
        # disable auto rewiring
        pass

    async def setup(self, graph: nx.Graph):
        print(f"{time.monotonic_ns()}\tmeshsim\tsetup\t", flush=True)
        # start a server for each node of the first graph.
        tasks = []
        input_ids = []
        for n in graph.nodes:
            input_ids.append(n)
            s = Server(0, 0) # TODO: set position based on a layout instead of hardcoding?
            tasks.append(self.add_server(s))
        servers = await asyncio.gather(*tasks)
        for i, s in enumerate(servers):
            self.input_id2id[input_ids[i]] = s.id

        # TODO: do not hardcode node positions and let webui arrange them
        # based on a dynamic spring based layout algorithm.
        positions = nx.circular_layout(self.graph, center=[300,300], scale=280)
        for id, [x, y] in positions.items():
                server = self.servers[id]
                server.x = x
                server.y = y

        # wire initial graph.
        await self.rewire(graph)

        # sleep for two seconds to let Synapse startup on each server.
        await asyncio.sleep(2)


    async def run(self, graphs: list[nx.Graph], period: float = 1.0) -> None:
        print(f"{time.monotonic_ns()}\tmeshsim\trun\t", flush=True)
        try:
            # update wiring for each period of time.
            for graph in graphs:
                await asyncio.gather(
                        self.rewire(graph),
                        asyncio.sleep(period),
                        )
        except asyncio.CancelledError:
            pass


    async def rewire(self, input_graph: nx.Graph):
        app.logger.info(input_graph)

        started_servers = self.get_started_servers()

        # reset neighbours from the server's point of view.
        # only for servers which have started up and have IPs
        for server in started_servers.values():
            server.reset_neighbours()

        # remove all edges from the underlying graph.
        self.graph.remove_edges_from(list(self.graph.edges()))

        for source, target, data in input_graph.edges(data=True):
            source_id = self.input_id2id[source]
            target_id = self.input_id2id[target]
            source_server = self.get_server(source_id)
            target_server = self.get_server(target_id)
            source_server.connect(target_server)
            # Copy the edge to self.graph, using "weight" as the weight attribute
            self.graph.add_edge(source_id, target_id, weight=data.get("weight", 1))

        self.paths = nx.shortest_path(self.graph, weight="weight")
        self.path_costs = dict(nx.shortest_path_length(self.graph, weight="weight"))

        # app.logger.info("calculated shortest paths as %r", self.paths)
        asyncio.create_task(self._do_rewire(started_servers, self.paths, self.path_costs))
        if event_notif_queue:
            await event_notif_queue.put({ "event_type": "update" })

    def get_bandwidth(self, server1, server2):
        """Returns the bandwidth, derivated from the weight attribute"""
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp
        data = self.graph.get_edge_data(server1.id, server2.id)
        weight = data.get("weight", 1)
        return max(self.min_bandwidth, int((1.1-weight) * self.bandwidth))

    def get_latency(self, server1, server2):
        """Returns the latency, derivated from the weight attribute"""
        if server1.id > server2.id:
            tmp = server1
            server1 = server2
            server2 = tmp
        data = self.graph.get_edge_data(server1.id, server2.id)
        weight = data.get("weight", 1)

        return int(weight * self.max_latency) * (self.latency_scale / 100)


mesh = Mesh("")


@app.route("/")
async def send_index():
    return await send_from_directory("", "index.html")


@app.route("/static/<path:filename>")
async def send_static(filename):
    return await send_from_directory("static/", filename)


@app.route("/server", methods=["POST"])
async def on_add_server():
    # {
    #   "x": 120,
    #   "y": 562
    # }
    incoming_json = await request.get_json()
    if not incoming_json:
        abort(400, "No JSON provided!")
        return

    x = incoming_json.get("x")
    y = incoming_json.get("y")

    server = await mesh.add_server(Server(x, y))
    return jsonify({"id": server.id})


@app.route("/server/<server_id>/position", methods=["PUT"])
async def on_position_server(server_id):
    # {
    #   "x": 120,
    #   "y": 562
    # }
    server_id = int(server_id)
    incoming_json = await request.get_json()
    if not incoming_json:
        abort(400, "No JSON provided!")
        return

    x = incoming_json.get("x")
    y = incoming_json.get("y")

    server = mesh.get_server(server_id)
    await mesh.move_server(server, x, y)

    return ""


def name_to_id(name):
    return int(name.replace("synapse", ""))


@app.route("/log", methods=["GET"])
async def on_incoming_log():
    args = request.args
    server = args["server"]
    msg = args["msg"]
    if msg == "ReceivedPDU":
        event_id = args["event_id"]
        origin = args["origin"]
        app.logger.info(f"Received {event_id}. {origin} -> {server}")
        print(f"{time.monotonic_ns()}\t{server}\trecv\t{event_id}", flush=True)
        await event_notif_queue.put(
            {
                "event_type": "receive",
                "source": origin,
                "target": server,
                "event": event_id,
            }
        )
    elif msg == "SendingPDU":
        event_id = args["event_id"]
        destinations = json.loads(args["destinations"])
        print(f"{time.monotonic_ns()}\t{server}\tsend\t{event_id}", flush=True)
        for destination in destinations:
            app.logger.info(f"{server} Sending {event_id}. {server} -> {destination}")
            try:
                await event_notif_queue.put(
                    {
                        "event_type": "sending",
                        "source": server,
                        "target": destination,
                        "path": mesh.get_path(name_to_id(server), name_to_id(destination)),
                        "event": event_id,
                    }
                )
            except:
                app.logger.warning(f"Fail to send notify for {event_id}. {server} -> {destination}")
    return ""


@app.websocket("/event_notifs")
async def event_notifs():
    await websocket.send("{}")
    while True:
        msg = await event_notif_queue.get()
        await websocket.send(json.dumps(msg))


@app.route("/data", methods=["GET"])
def on_get_data():
    return mesh.get_d3_data()


@app.route("/costs", methods=["GET"])
def on_get_costs():
    return jsonify(mesh.get_costs())


@app.route("/defaults", methods=["GET"])
def on_get_defaults():
    return jsonify(mesh.get_defaults())


@app.route("/defaults", methods=["PUT"])
async def on_put_defaults():
    json = await request.get_json()
    mesh.set_defaults(json)
    await mesh.safe_auto_rewire()
    return ""


@app.route("/link/<server1>/<server2>/<type>", methods=["PUT"])
async def on_put_link_health(server1, server2, type):
    json = await request.get_json()
    mesh.set_link_health(int(server1), int(server2), json)
    await mesh.safe_auto_rewire()
    return ""


@app.route("/link/<server1>/<server2>/<type>", methods=["DELETE"])
async def on_delete_link_health(server1, server2, type):
    json = {}
    json[type] = None
    mesh.set_link_health(int(server1), int(server2), json)
    await mesh.safe_auto_rewire()
    return ""


def cleanup():
    subprocess.call(["./stop_clean_all.sh", str(id)], stdout=stdout, stderr=stderr)


def parse_graphml(path: str, skip_str: str) -> list[nx.Graph]:
    count_str, out_of_str = skip_str.split('/', 2)
    count = int(count_str)
    out_of = int(out_of_str)
    threshold = out_of - count
    paths = []
    for (i, p) in enumerate(sorted(os.scandir(path), key=lambda d: d.name)):
        if i % out_of >= threshold:
            continue
        paths.append(p)
    graphs = []
    for file in tqdm(paths):
        try:
            graph: nx.Graph = nx.read_graphml(file)
            graphs.append(graph)
        except Exception as e:
            tqdm.write(f'warning: skipping {file.name}: invalid file: {e}')
            continue
    if len(graphs) == 0:
        raise Exception('no valid graphml files')
    return graphs


async def check_proc(proc: Process):
    code = await proc.wait()
    if code != 0:
        raise Exception(f"subprocess exited with non-zero code: {code}")


async def main():
    global args, id, host

    parser = argparse.ArgumentParser(description="Synapse network simulator.")
    parser.add_argument(
        "id", help="The numerical ID to use for the docker network and the listeners ports [0-9].",
        choices=range(10),
        type=int,
    )
    parser.add_argument(
        "graphmldir",
        help="Directory containing input network GraphML files.",
        nargs='?',
    )
    parser.add_argument(
        "--run",
        "-r",
        help="Command to run during the experiment",
        type=str,
    )
    parser.add_argument(
        "--setup",
        "-s",
        help="Command to run before running the experiment",
        type=str,
    )
    parser.add_argument(
        "--jaeger",
        "-j",
        help="Enable Jaeger tracing in Synapse and CoAP proxy",
        action="store_true",
    )
    parser.add_argument(
        "--no-proxy",
        "-n",
        help="Have Synapse talk directly to each other rather than via the CoAP proxy",
        action="store_false",
        dest="use_proxy",
    )
    parser.add_argument(
        "--proxy-dump-payloads",
        help="Debug option to make the CoAP proxy log the packets that are being sent/received",
        action="store_true",
    )
    parser.add_argument(
        "--period",
        help="Delay in seconds between each graph of the graphml directory",
        type=float,
        default=1.0,
    )
    parser.add_argument(
        "--timeout",
        help="Delay in seconds before stopping the experiment",
        type=float,
    )
    parser.add_argument(
        "--skip",
        help="Fraction od graphs to skip from the graphml directory (e.g. 3/4)",
        default="0/2",
    )
    parser.add_argument(
        "--debug",
        help="Enable debug logging",
        action="store_true",
    )
    args = parser.parse_args()

    id = args.id
    host = subprocess.check_output(["./create_network.sh", str(id)], stderr=sys.stderr, text=True).strip()
    os.environ["POSTGRES_HOST"] = host
    os.environ["SYNAPSE_LOG_HOST"] = host

    if args.jaeger:
        os.environ["SYNAPSE_JAEGER_HOST"] = host

    if args.use_proxy:
        os.environ["SYNAPSE_USE_PROXY"] = "1"

    if args.proxy_dump_payloads:
        os.environ["PROXY_DUMP_PAYLOADS"] = "1"

    if args.debug:
        global stdout, stderr
        stdout = sys.stderr
        stderr = sys.stderr

    atexit.register(cleanup)
    tasks = []
    procs = []
    if args.graphmldir != None:
        global mesh
        mesh = DynMesh(host)
        graphs = parse_graphml(args.graphmldir, args.skip)
        await asyncio.create_task(mesh.setup(graphs[0]))
        if args.setup:
            proc = await asyncio.create_subprocess_shell(args.setup, stdout=sys.stderr)
            await check_proc(proc)
            # Wait to make sure setup is complete.
            await(asyncio.sleep(1))
        tasks.append(asyncio.create_task(mesh.run(graphs[1:], args.period)))
        if args.run:
            # If pgid is zero, then the PGID of the process specified by pid is made the same as its process ID
            proc = await asyncio.create_subprocess_shell(args.run, stdout=sys.stderr, process_group=0)
            procs.append(proc)
            tasks.append(check_proc(proc))
    # Quart's run_task catches Ctrl+C interrupt and exits cleanly
    # so we can't use a TaskGroup to cancel the other tasks when
    # it exits. Instead, we register a callback to cancel all the
    # running tasks manually.
    server_task = asyncio.create_task(app.run_simple_task(port=3000 + id*100, debug=args.debug))
    tasks.append(server_task)
    futures = asyncio.gather(*tasks)
    server_task.add_done_callback(lambda _: futures.cancel())
    try:
        if args.timeout:
            await asyncio.wait_for(futures, args.timeout)
        else:
            await futures
    except (asyncio.CancelledError, TimeoutError):
        pass

    for proc in procs:
        try:
            os.killpg(proc.pid, signal.SIGINT)
        except ProcessLookupError:
            print(f"proc {proc.pid} already terminated", file=sys.stderr)
        await proc.wait()


if __name__ == "__main__":
    asyncio.run(main())
