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
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/felixge/httpsnoop"
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

// writeFiles attempts to write the multiple given files, returning the first
// error encountered.
// A file is an array of two string, its path, and its content to write.
func writeFiles(files [][2]string) error {
	for _, f := range files {
		if err := os.WriteFile(f[0], []byte(f[1]), 0644); err != nil {
			return err
		}
	}
	return nil
}

// configureRouting configures the current node as a simulated mesh router.
func configureRouting() error {
	return writeFiles([][2]string{
		// Turn this node into a router
		{"/proc/sys/net/ipv4/conf/eth0/forwarding", "1"},
		// Prevent sending ICMP redirects, as we hard wire the routes
		{"/proc/sys/net/ipv4/conf/eth0/send_redirects", "0"},
	})
}

// logError writes an error message contructed from the given format and args
// both to the logs and to the response writer, setting the response code by
// the way.
func logError(w http.ResponseWriter, code int, format string, args ...any) {
	msg := fmt.Sprintf("ERROR: "+format, args...)
	log.Println(msg)
	http.Error(w, msg, code)
}

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
	log.Print("adding new peer: ", id)
	peersLock.Lock()
	defer peersLock.Unlock()
	if !peers[id] {
		if err := addPeerLink(id); err != nil {
			logError(w, 500, "add peer %d: %v", id, err)
			return false
		}
		peers[id] = true
	}
	log.Print("added new peer: ", id)
	return true
}

// decodeJSON attempts to decode r into the given val.
//
// On error, it writes the error message to w and returns false.
func decodeJSON(w http.ResponseWriter, r io.Reader, val any, msg string) bool {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(val); err != nil {
		logError(w, 400, "%s: %v", msg, err)
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
				logError(w, 500, "del peer %d route: %v", route.Dst.ID, err)
				return
			}
		} else {
			if !addPeerIfNew(w, route.Via.ID) {
				return
			}
			if err := setPeerRoutes(route.Dst, route.Via); err != nil {
				logError(w, 500, "set peer %d route: %v", route.Dst.ID, err)
				return
			}
		}
	}
	if err := dumpRoutes(w); err != nil {
		logError(w, 500, "dump routes: %v", err)
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
			logError(w, 500, "change peer %d qdisc: %v", id, err)
			return
		}
	}
	if err := dumpQdiscs(w); err != nil {
		logError(w, 500, "dump qdisc: %v", err)
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	port := flag.Int("p", 3000, "Listening port")
	flag.Parse()

	if err := configureRouting(); err != nil {
		log.Fatalf("configure routing: %v", err)
	}
	if err := initRoutes(); err != nil {
		log.Fatalf("init routes: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /routes", handleRoutes)
	mux.HandleFunc("PUT /health", handleHealth)
	log.Printf("launching server on http://0.0.0.0:%d", *port)

	logHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("request:  %s %s", r.Method, r.URL)
		m := httpsnoop.CaptureMetrics(mux, w, r)
		log.Printf("response: %s %s code=%d dt=%s", r.Method, r.URL, m.Code, m.Duration)
	})
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), logHandler))
}
