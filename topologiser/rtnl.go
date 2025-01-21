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
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const (
	linkFormat = "peer%d"
	rootQdisc  = 0xFFFFFFFF
	milisecond = 1_000_000
)

var (
	ipnl = ipnlDial()
	tcnl = tcnlDial()
	dev  = defaultInterface()

	latencyDefault int64 = 0
)

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

// defaultInterface returns the first non-loopback interface found, or else panics.
func defaultInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		panic("get interfaces: " + err.Error())
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback == 0 {
			return &iface
		}
	}
	panic("no valid interface found")
}

// findDefaultRoute tries to find the current default route.
func findDefaultRoute(routes []rtnetlink.RouteMessage) (*rtnetlink.RouteMessage, error) {
	for _, r := range routes {
		if r.Attributes.Dst == nil {
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

// listIPv4Routes retrieves only the IPv4 routes from the main routing table,
// for the given interface.
func listIPv4Routes(iface *net.Interface) ([]rtnetlink.RouteMessage, error) {
	req := &rtnetlink.RouteMessage{
		Family:     unix.AF_INET,
		Scope:      unix.RT_SCOPE_UNIVERSE,
		Table:      unix.RT_TABLE_MAIN,
		Attributes: rtnetlink.RouteAttributes{OutIface: uint32(iface.Index)},
	}
	flags := netlink.Request | netlink.Dump
	msgs, err := ipnl.Execute(req, unix.RTM_GETROUTE, flags)
	routes := make([]rtnetlink.RouteMessage, len(msgs))
	for i := range msgs {
		routes[i] = *msgs[i].(*rtnetlink.RouteMessage)
	}
	return routes, err
}

// filterUnicast returns only the unicast routes from the given ones.
func filterUnicast(routes []rtnetlink.RouteMessage) (filtered []rtnetlink.RouteMessage) {
	for _, r := range routes {
		if r.Type == unix.RTN_UNICAST {
			filtered = append(filtered, r)
		}
	}
	return
}

// delRoutes deletes all the given routes, stopping at the first error other than
// "no such process", as it is the error returned when the route to delete does
// not exist.
func delRoutes(routes []rtnetlink.RouteMessage) error {
	for _, r := range routes {
		if err := ipnl.Route.Delete(&r); err != nil {
			// Ignore "no such process" error
			if !errors.Is(err, unix.ESRCH) {
				return fmt.Errorf("del route %+v: %w: %#v", r, err, errors.Unwrap(err))
			}
		}
	}
	return nil
}

// setRoutes sets all the given routes, stopping at the first error.
//
// For each route, the attributes Family, Protocol and Table are overriden,
// so is is useless to set them beforehand.
func setRoutes(routes []rtnetlink.RouteMessage) error {
	for _, r := range routes {
		r.Family = unix.AF_INET
		r.Protocol = unix.RTPROT_BOOT
		r.Table = unix.RT_TABLE_MAIN
		if err := ipnl.Route.Replace(&r); err != nil {
			return fmt.Errorf("add route %+v: %w", r, err)
		}
	}
	return nil
}

// initRoutes prepares the routes of the container.
func initRoutes() error {
	routes, err := listIPv4Routes(dev)
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
	if err := delRoutes(filterUnicast(routes)); err != nil {
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
	if err := setRoutes(routes); err != nil {
		return fmt.Errorf("add routes: %w", err)
	}
	return nil
}

// addPeerLink creates a new macvlan link with a netem qdisc for a given peer.
func addPeerLink(peerID int) error {
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
		return fmt.Errorf("ip link add: %w", err)
	}

	// Set new link up
	link, err := net.InterfaceByName(linkName)
	if err != nil {
		return fmt.Errorf("interface %s: %w", linkName, err)
	}
	err = ipnl.Link.Set(&rtnetlink.LinkMessage{
		Index:  uint32(link.Index),
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
	})
	if err != nil {
		return fmt.Errorf("ip link set up: %w", err)
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
		return fmt.Errorf("tc qdisc add: %w", err)
	}

	return nil
}

// setPeerRoutes creates or replace the needed routes for the given peer.
func setPeerRoutes(dst, via *Peer) error {
	linkName := fmt.Sprintf(linkFormat, via.ID)
	link, err := net.InterfaceByName(linkName)
	if err != nil {
		return fmt.Errorf("interface %s: %w", linkName, err)
	}
	routes := []rtnetlink.RouteMessage{
		{ // Make sure there is a route to the gateway
			Scope:     unix.RT_SCOPE_LINK,
			Type:      unix.RTN_UNICAST,
			DstLength: 32,
			Attributes: rtnetlink.RouteAttributes{
				Dst:      via.IP,
				OutIface: uint32(link.Index),
			},
		},
		{ // Set the route for this peer
			Type:      unix.RTN_UNICAST,
			DstLength: 32,
			Attributes: rtnetlink.RouteAttributes{
				Dst:     dst.IP,
				Gateway: via.IP,
			},
		},
	}
	return setRoutes(routes)
}

// delPeerRoute deletes the route to the given peer.
func delPeerRoute(dst *Peer) error {
	routes := []rtnetlink.RouteMessage{{
		Family:     unix.AF_INET,
		DstLength:  32,
		Attributes: rtnetlink.RouteAttributes{Dst: dst.IP},
	}}
	return delRoutes(routes)
}

func changeQdisc(health Health) error {
	linkName := fmt.Sprintf(linkFormat, health.Peer.ID)
	link, err := net.InterfaceByName(linkName)
	if err != nil {
		return fmt.Errorf("interface %s: %w", linkName, err)
	}
	var (
		bandwidth = uint64(health.Bandwidth)
		latency   = int64(health.Latency)
		jitter    = int64(health.Latency)
	)
	qdisc := tc.Object{
		Msg: tc.Msg{
			Ifindex: uint32(link.Index),
			Parent:  rootQdisc,
		},
		Attribute: tc.Attribute{
			Kind: "netem",
			Netem: &tc.Netem{
				Rate64:    &bandwidth,
				Latency64: &latency,
				Jitter64:  &jitter,
				Corr: &tc.NetemCorr{
					Delay: 25,
				},
			},
		},
	}
	return tcnl.Qdisc().Change(&qdisc)
}
