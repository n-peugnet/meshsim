#!/usr/bin/env python

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

import os
from socket import AF_INET
from flask import Flask, request
import subprocess


abspath = os.path.abspath(__file__)
dname = os.path.dirname(abspath)
os.chdir(dname)

app = Flask(__name__, static_url_path='')


def run(cmd):
    out = subprocess.run(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        universal_newlines=True,
    )
    result = "\n>>> " + ' '.join(cmd)
    if out.stdout:
        result += "\n<<<\n" + out.stdout
    if out.stderr:
        result += "\n<!!\n" + out.stderr
    return result

def init():
    run(["./clear_hs_routes.sh"])

@app.route("/routes", methods=["PUT"])
def set_routes():
    # [
    #   {
    #       dst: server,
    #       via: server
    #   }, ...
    # ]
    routes = request.get_json()

    result = ''
    for route in routes:
        if route['via'] is None:
            result += run(["./del_hs_route.sh", route['dst']['ip']])
        else:
            result += run([
                "./add_hs_route.sh", route['dst']['ip'], route['via']['ip'],
            ])

    # Print result
    result += run(["ip", "route", "show"])

    return result

@app.route("/health", methods=["PUT"])
def set_network_health():
    # {
    #     peers: [
    #         {
    #             peer: <server>,
    #             bandwidth: 300, # 300bps
    #             latency: 200, # 200ms
    #             jitter: 20, # +/- 20ms - we apply 25% correlation on jitter
    #         }, ...
    #     ],
    #     clients: [
    #         {
    #             source_port: 54312,
    #             bandwidth: 300, # 300bps
    #             latency: 200, # 200ms
    #             jitter: 20, # +/- 20ms - we apply 25% correlation on jitter
    #         }, ...
    #     ]
    # }
    json = request.get_json()

    i = 2  # we start adding the queues from 1:2, as 1:1 is the default queue
    flow_count = len(json['peers']) + len(json['clients']) + 1
    if flow_count < 2:
        flow_count = 2

    result = ''
    result += run(
        ["./clear_hs_peer_health.sh", str(flow_count)]
    )
    for peer in json['peers']:
        mac = peer['peer']['mac'].split(":")
        result += run(
            ["./set_hs_peer_health.sh", str(i)] +
            mac +
            [str(peer['bandwidth']), str(peer['latency']), str(peer['jitter'])]
        )
        i = i + 1

    for client in json['clients']:
        result += run(
            ["./set_client_health.sh", str(i), str(client.get('source_port', 0))] +
            [str(client['bandwidth']), str(client['latency']), str(client['jitter'])]
        )

    return result

init()
app.run(host="0.0.0.0", port=3000, debug=True)
