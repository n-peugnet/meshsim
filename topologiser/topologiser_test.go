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
	"io"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
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

	if err := exec.Command("make", "dummynet").Run(); err != nil {
		t.Fatal(err)
	}
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
		{
			"basic route",
			`[
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
]`,
			`routes:
default via 172.22.0.1 dev eth0
172.22.0.0/24 dev eth0 src 172.22.0.3
172.22.0.4 dev peer1
172.22.0.5 via 172.22.0.4 dev peer1
`, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subTestHandleRoutes(t, c.req, c.res, c.code)
		})
	}
}

func subTestHandleRoutes(t *testing.T, request, expected string, code int) {
	setupNetNS(t)
	r := strings.NewReader(request)
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
