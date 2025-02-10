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
	"bytes"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"testing"

	"github.com/florianl/go-tc"
	"github.com/jsimonetti/rtnetlink/v2"
	"github.com/mdlayher/netlink"
	"github.com/vishvananda/netns"
)

func setupNetNS(t *testing.T) {
	// Lock the OS Thread so we don't accidentally switch namespaces
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	// Save the current network namespace
	origns, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { origns.Close() })

	// Create a new network namespace
	newns, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { newns.Close() })

	// Use the newly created namespace
	if err := netns.Set(newns); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netns.Set(origns) })

	ipnl, err = rtnetlink.Dial(&netlink.Config{NetNS: int(newns)})
	if err != nil {
		t.Fatal(err)
	}
	tcnl, err = tc.Open(&tc.Config{NetNS: int(newns)})
	if err != nil {
		t.Fatal(err)
	}
	for k := range peers {
		delete(peers, k)
	}

	if err := exec.Command("make", "dummynet").Run(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureRouting(t *testing.T) {
	configureRouting()
	expected := []byte("1\n")
	actual, _ := os.ReadFile("/proc/sys/net/ipv4/conf/eth0/forwarding")
	if !bytes.Equal(actual, expected) {
		t.Errorf("expected forwarding to be %q, got: %q", expected, actual)
	}
	if os.Getenv("CI") == "true" {
		t.Skip("send_redirects cannot be disabled on GitLab CI for an uknown reason")
	}
	expected = []byte("0\n")
	actual, _ = os.ReadFile("/proc/sys/net/ipv4/conf/eth0/send_redirects")
	if !bytes.Equal(actual, expected) {
		t.Errorf("expected send_redirects to be %q, got: %q", expected, actual)
	}
}

func TestInitRoutes(t *testing.T) {
	buf := bytes.Buffer{}
	dumpRoutes(&buf)
	expected := `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
`
	actual := buf.String()
	if actual != expected {
		t.Errorf("expected routes before initRoute:\n%sactual:\n%s", expected, actual)
	}

	initRoutes()
	buf.Reset()
	dumpRoutes(&buf)
	expected = `routes:
default via 172.22.0.1 dev eth0
unreachable 172.22.0.0/24 dev 0
172.22.0.1 dev eth0
172.22.0.2 dev eth0
`
}

func TestHandleRoutes(t *testing.T) {
	cases := []struct {
		name string
		req  string
		res  string
		code int
	}{
		{"empty routes", "[]", `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
`, 200},
		{"basic route", `[
    {
        "dst": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        },
        "via": {
            "id": 1,
            "ip": "172.22.0.4",
            "mac": "02:42:ac:16:00:04"
        },
        "cost": 72.0
    }
]`, `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.4 dev peer1
172.22.0.5 via 172.22.0.4 dev peer1
`, 200},
		{"direct link", `[
    {
        "dst": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        },
        "via": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        },
        "cost": 72.0
    }
]`, `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.5 via 172.22.0.5 dev peer2
`, 200},
		{"remove non-existing", `[
    {
        "dst": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        }
    }
]`, `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
`, 200},
		{"invalid json", `[`, "decode routes: unexpected EOF\n", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupNetNS(t)
			requestHandleRoutes(t, c.req, c.res, c.code)
		})
	}
}

func requestHandleRoutes(t *testing.T, request, expected string, code int) {
	r := bytes.NewReader([]byte(request))
	req := httptest.NewRequest("PUT", "http://localhost:3000/routes", r)
	w := httptest.NewRecorder()
	handleRoutes(w, req)

	res := w.Result()
	if res.StatusCode != code {
		t.Errorf("expected %d status code, got: %d", code, res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != expected {
		t.Errorf("expected body:\n%sactual:\n%s", expected, body)
	}
}

func TestHandleHealth(t *testing.T) {
	cases := []struct {
		name string
		req  string
		res  string
		code int
	}{
		{"empty health", `{"peers":[]}`, `qdiscs:
qdisc noqueue 0:0 dev eth0
`, 200},
		{"basic health", `{
    "peers": [
        {
            "peer": {
                "id": 4,
                "ip": "172.22.0.7",
                "mac": "02:42:ac:16:00:07"
            },
            "bandwidth": 1433244,
            "latency": 72.0,
            "jitter": 0,
            "packet_loss": 0
        }
    ]
}`, `qdiscs:
qdisc noqueue 0:0 dev eth0
qdisc netem \d+:0 dev peer4 limit 1000 delay 72ms rate 1433kb/s
`, 200},
		{"advanced health", `{
    "peers": [
        {
            "peer": {
                "id": 4,
                "ip": "172.22.0.7",
                "mac": "02:42:ac:16:00:07"
            },
            "bandwidth": 14332440000,
            "latency": 105.1,
            "jitter": 12,
            "packet_loss": 10
        }
    ]
}`, `qdiscs:
qdisc noqueue 0:0 dev eth0
qdisc netem \d+:0 dev peer4 limit 1000 delay 105.1ms jitter 12ms 25% rate 14332Mb/s
`, 200},
		{"64bits rate", `{
    "peers": [
        {
            "peer": {
                "id": 4,
                "ip": "172.22.0.7",
                "mac": "02:42:ac:16:00:07"
            },
            "bandwidth": 1433244000000,
            "latency": 2,
            "jitter": 0,
            "packet_loss": 0
        }
    ]
}`, `qdiscs:
qdisc noqueue 0:0 dev eth0
qdisc netem \d+:0 dev peer4 limit 1000 delay 2ms rate 1433Gb/s
`, 200},
		{"invalid json", `[`, "decode health: unexpected EOF\n", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupNetNS(t)
			requestHandleHealth(t, c.req, c.res, c.code)
		})
	}
}

func requestHandleHealth(t *testing.T, request, expected string, code int) {
	r := bytes.NewReader([]byte(request))
	req := httptest.NewRequest("PUT", "http://localhost:3000/health", r)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	res := w.Result()
	if res.StatusCode != code {
		t.Errorf("expected %d status code, got: %d", code, res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	match, _ := regexp.Match(expected, body)
	if !match {
		t.Errorf("expected body to match:\n%sactual:\n%s", expected, body)
	}
}

func ifaceExist(t *testing.T, name string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Error("unexpected error: ", err)
	}
	for _, iface := range ifaces {
		if iface.Name == name {
			return true
		}
	}
	return false
}

func TestCompoundRoutes(t *testing.T) {
	setupNetNS(t)
	peer1 := "peer1"
	peer2 := "peer2"

	// Check interfaces right after setup
	if ifaceExist(t, peer1) {
		t.Errorf("interface should not exist: %s", peer1)
	}

	// Add route to peer2 via peer1
	routes := `[
    {
        "dst": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        },
        "via": {
            "id": 1,
            "ip": "172.22.0.4",
            "mac": "02:42:ac:16:00:04"
        }
    }
]`
	expected := `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.4 dev peer1
172.22.0.5 via 172.22.0.4 dev peer1
`
	requestHandleRoutes(t, routes, expected, 200)
	if !ifaceExist(t, peer1) {
		t.Errorf("interface should exist: %s", peer1)
	}

	// Add direct link to peer1
	routes = `[
    {
        "dst": {
            "id": 1,
            "ip": "172.22.0.4",
            "mac": "02:42:ac:16:00:04"
        },
        "via": {
            "id": 1,
            "ip": "172.22.0.4",
            "mac": "02:42:ac:16:00:04"
        }
    }
]`
	expected = `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.4 via 172.22.0.4 dev peer1
172.22.0.5 via 172.22.0.4 dev peer1
`
	requestHandleRoutes(t, routes, expected, 200)

	// Change route to peer2 into direct link
	routes = `[
    {
        "dst": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        },
        "via": {
            "id": 2,
            "ip": "172.22.0.5",
            "mac": "02:42:ac:16:00:05"
        }
    }
]`
	expected = `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.4 via 172.22.0.4 dev peer1
172.22.0.5 via 172.22.0.5 dev peer2
`
	requestHandleRoutes(t, routes, expected, 200)
	if !ifaceExist(t, peer2) {
		t.Errorf("interface should exist: %s", peer2)
	}

	// Remove route to peer
	routes = `[
    {
        "dst": {
            "id": 1,
            "ip": "172.22.0.4",
            "mac": "02:42:ac:16:00:04"
        }
    }
]`
	expected = `routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.5 via 172.22.0.5 dev peer2
`
	requestHandleRoutes(t, routes, expected, 200)
	// This should not delete the interface
	if !ifaceExist(t, peer1) {
		t.Errorf("interface should exist: %s", peer1)
	}
}
