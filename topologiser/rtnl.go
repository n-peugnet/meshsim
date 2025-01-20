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
	"errors"
	"fmt"
	"net"

	"github.com/florianl/go-tc"
	"github.com/jsimonetti/rtnetlink/v2"
	"golang.org/x/sys/unix"
)

const (
	devName    = "wlp4s0"
	linkFormat = "peer%d"
	rootQdisc  = 0xFFFFFFFF
	milisecond = 1_000_000
)

var (
	ipnl = ipnlDial()
	tcnl = tcnlDial()
	dev  *net.Interface

	latencyDefault int64 = 0
)

func init() {
	var err error
	dev, err = net.InterfaceByName(devName)
	if err != nil {
		panic("interface " + devName + ": " + err.Error())
	}
}

// ipnlDial opens a connection to the rtnetlink socket for ip-like operations.
func ipnlDial() *rtnetlink.Conn {
	ipnl, err := rtnetlink.Dial(nil)
	if err != nil {
		panic("rtnetlink ip: " + err.Error())
	}
	return ipnl
}

// tcnlDial opens a connection to the rtnetlink socket for tc-like operations.
func tcnlDial() *tc.Tc {
	tcnl, err := tc.Open(&tc.Config{})
	if err != nil {
		panic("rtnetlink tc: " + err.Error())
	}
	return tcnl
}

// findDefaultRoute tries to find the current default route.
func findDefaultRoute(routes []rtnetlink.RouteMessage) (*rtnetlink.RouteMessage, error) {
	for _, r := range routes {
		if r.Family == unix.AF_INET && r.Attributes.Dst == nil && r.Attributes.OutIface == uint32(dev.Index) {
			return &r, nil
		}
	}
	return nil, errors.New("default route not found")
}

// findGateway tries to find the gateway of the current default route.
func findGateway(routes []rtnetlink.RouteMessage) (net.IP, error) {
	r, err := findDefaultRoute(routes)
	if err != nil {
		return nil, err
	}
	return r.Attributes.Gateway, nil
}

// findIPv4 tries to find the current IPv4.
func findIPv4() (*net.IPNet, error) {
	addrs, err := dev.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ipnet := addr.(*net.IPNet)
		if ipnet.IP.To4() != nil {
			return ipnet, nil
		}
	}
	return nil, errors.New("ipv4 not found")
}

// delRoutes deletes all the given routes, stopping at the first error.
func delRoutes(routes []rtnetlink.RouteMessage) error {
	for _, r := range routes {
		if err := ipnl.Route.Delete(&r); err != nil {
			return fmt.Errorf("del route %+v: %w", r, err)
		}
	}
	return nil
}

// addRoutes adds all the given routes, stopping at the first error.
//
// For each route, the attributes Family, Protocol and Table are overriden,
// so is is useless to set them beforehand.
func addRoutes(routes []rtnetlink.RouteMessage) error {
	for _, r := range routes {
		r.Family = unix.AF_INET
		r.Protocol = unix.RTPROT_BOOT
		r.Table = unix.RT_TABLE_MAIN
		if err := ipnl.Route.Add(&r); err != nil {
			return fmt.Errorf("add route %+v: %w", r, err)
		}
	}
	return nil
}

// initRoutes prepares the routes of the container.
func initRoutes() error {
	routes, err := ipnl.Route.List()
	if err != nil {
		return fmt.Errorf("ip route list: %w", err)
	}
	gateway, err := findGateway(routes)
	if err != nil {
		return fmt.Errorf("find gateway: %w", err)
	}
	ipnet, err := findIPv4()
	if err != nil {
		return fmt.Errorf("find network: %w", err)
	}
	prefix, _ := ipnet.Mask.Size()
	network := ipnet.IP.Mask(ipnet.Mask)
	if err := delRoutes(routes); err != nil {
		return fmt.Errorf("flush routes: %w", err)
	}
	routes = []rtnetlink.RouteMessage{
		{ // Add a route to the gateway to override the blackhole
			Scope:     unix.RT_SCOPE_LINK,
			Type:      unix.RTN_UNICAST,
			DstLength: 32,
			Attributes: rtnetlink.RouteAttributes{
				Dst:      gateway,
				OutIface: uint32(dev.Index),
			},
		},
		{ // By default, blackhole all the address of the current network
			Type:      unix.RTN_BLACKHOLE,
			DstLength: uint8(prefix),
			Attributes: rtnetlink.RouteAttributes{
				Dst: network,
			},
		},
		{ // Add a default route for the rest of the traffic
			Type: unix.RTN_UNICAST,
			Attributes: rtnetlink.RouteAttributes{
				Dst:     nil,
				Gateway: gateway,
			},
		},
	}
	if err := addRoutes(routes); err != nil {
		return fmt.Errorf("add routes: %w", err)
	}
	return nil
}

// addPeerLink creates a new macvlan link with a netem qdisc for a given peer.
//
// It returns the created qdisc, to be able to easily modify it later.
func addPeerLink(peerID int) (*tc.Object, error) {
	linkName := fmt.Sprintf(linkFormat, peerID)

	// Create new macvlan link
	err := ipnl.Link.New(&rtnetlink.LinkMessage{
		Attributes: &rtnetlink.LinkAttributes{
			Name: linkName,
			Type: uint32(dev.Index),
			Info: &rtnetlink.LinkInfo{
				Kind: "macvlan",
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ip link add: %w", err)
	}

	// Set new link up
	link, err := net.InterfaceByName(linkName)
	if err != nil {
		panic("interface " + devName + ": " + err.Error())
	}
	err = ipnl.Link.Set(&rtnetlink.LinkMessage{
		Index:  uint32(link.Index),
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
	})
	if err != nil {
		return nil, fmt.Errorf("ip link set up: %w", err)
	}

	// Add netem qdisc with no limitations
	qdisc := &tc.Object{
		Msg: tc.Msg{
			Ifindex: uint32(link.Index),
			Parent:  rootQdisc,
		},
		Attribute: tc.Attribute{
			Kind: "netem",
			Netem: &tc.Netem{
				Latency64: &latencyDefault,
			},
		},
	}
	if err := tcnl.Qdisc().Add(qdisc); err != nil {
		return nil, fmt.Errorf("tc qdisc add: %w", err)
	}

	return qdisc, nil
}
