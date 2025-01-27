// Copyright 2025 Nicolas Peugnet <nicolas.peugnet@lip6.fr>
//
// This file is part of meshsim.
//
// meshsim is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// meshsim is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with meshsim.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
)

type Peer struct {
	ID  int    `json:"id"`
	IP  net.IP `json:"ip"`
	MAC string `json:"mac"`
}

type Route struct {
	Dst  *Peer   `json:"dst"`
	Via  *Peer   `json:"via"`
	Cost float32 `json:"cost"`
}

type Health struct {
	Peer       Peer    `json:"peer"`
	Bandwidth  float32 `json:"bandwidth"`   // in bits per second
	Latency    float32 `json:"latency"`     // in milliseconds
	Jitter     float32 `json:"jitter"`      // in milliseconds
	PacketLoss float32 `json:"packet_loss"` // in percentage
}

var (
	peers     = make(map[int]bool, 0)
	peersLock sync.RWMutex
)

// addPeerIfNew checks if the peer has already been added, if not, it adds it
// to the known peers and creates a new virtual link that will be used to set
// the link health for this peer.
//
// On error, it writes the error message to w and returns false.
func addPeerIfNew(w http.ResponseWriter, id int) bool {
	peersLock.RLock()
	known := peers[id]
	peersLock.RUnlock()
	if known {
		return true
	}
	peersLock.Lock()
	defer peersLock.Unlock()
	if !peers[id] {
		if err := addPeerLink(id); err != nil {
			http.Error(w, "add peer: "+err.Error(), 500)
			return false
		}
		peers[id] = true
	}
	return true
}

// decodeJSON attempts to decode r into the given val.
//
// On error, it writes the error message to w and returns false.
func decodeJSON(w http.ResponseWriter, r io.Reader, val any, msg string) bool {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(val); err != nil {
		http.Error(w, msg+": "+err.Error(), 400)
		return false
	}
	return true
}

// handleRoutes sets the routes of the current node.
//
// Example body:
//
//	[
//	    {
//	        "dst": {
//	            "id": 2,
//	            "ip": "172.22.0.4",
//	            "mac": "02:42:ac:16:00:04"
//	        },
//	        "via": {
//	            "id": 1,
//	            "ip": "172.22.0.3",
//	            "mac": "02:42:ac:16:00:03"
//	        },
//	        "cost": 72.0
//	    }
//	]
func handleRoutes(w http.ResponseWriter, r *http.Request) {
	var routes []Route
	if !decodeJSON(w, r.Body, &routes, "decode routes") {
		return
	}
	for _, route := range routes {
		if route.Via == nil {
			if err := delPeerRoute(route.Dst); err != nil {
				http.Error(w, "del peer route: "+err.Error(), 500)
				return
			}
		} else {
			if !addPeerIfNew(w, route.Via.ID) {
				return
			}
			if err := setPeerRoutes(route.Dst, route.Via); err != nil {
				http.Error(w, "set peer route: "+err.Error(), 500)
				return
			}
		}
	}
	if err := dumpRoutes(w); err != nil {
		http.Error(w, "dump routes: "+err.Error(), 500)
	}
}

// handleHealth sets the health of the links of the current node.
//
// Example body:
//
//	{
//	    "peers": [
//	        {
//	            "peer": {
//	                "id": 0,
//	                "ip": "172.22.0.2",
//	                "mac": "02:42:ac:16:00:02"
//	            },
//	            "bandwidth": 1433244,
//	            "latency": 72.0,
//	            "jitter": 0,
//	            "packet_loss": 0
//	        }
//	    ]
//	}
func handleHealth(w http.ResponseWriter, r *http.Request) {
	var health struct {
		Peers []Health
	}
	if !decodeJSON(w, r.Body, &health, "decode health") {
		return
	}
	for _, peer := range health.Peers {
		id := peer.Peer.ID
		if !addPeerIfNew(w, id) {
			return
		}
		if err := changeQdisc(peer); err != nil {
			http.Error(w, "change qdisc: "+err.Error(), 500)
			return
		}
	}
	if err := dumpQdiscs(w); err != nil {
		http.Error(w, "dump qdiscs: "+err.Error(), 500)
	}
}

func main() {
	if err := initRoutes(); err != nil {
		log.Fatalf("init routes: %v", err)
	}

	http.HandleFunc("PUT /routes", handleRoutes)
	http.HandleFunc("PUT /health", handleHealth)
	log.Println("launching server on http://0.0.0.0:3000")
	log.Fatal(http.ListenAndServe(":3000", nil))
}
