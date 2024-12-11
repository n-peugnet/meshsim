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
from threading import Lock
from flask import Flask, request
import subprocess


abspath = os.path.abspath(__file__)
dname = os.path.dirname(abspath)
os.chdir(dname)

app = Flask(__name__, static_url_path='')
peers = set()
peers_lock = Lock()


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

def create_peer_if_new(id):
    with peers_lock:
        if id not in peers:
            peers.add(id)
            return run(["./add_hs_peer.sh", str(id)])
    return ""

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
            id = route['via']['id']
            result += create_peer_if_new(id)
            result += run([
                "./add_hs_route.sh", route['dst']['ip'], route['via']['ip'], str(id)
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
    # }
    json = request.get_json()

    result = ''
    for peer in json['peers']:
        id = peer['peer']['id']
        result += create_peer_if_new(id)
        result += run([
            "./set_hs_peer_health.sh", str(id),
            str(peer['bandwidth']), str(peer['latency']), str(peer['jitter'])
        ])

    # Print result
    result += run(["tc", "qdisc", "show"])

    return result

init()
app.run(host="0.0.0.0", port=3000, debug=True)
