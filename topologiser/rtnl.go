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
	"io"
	"math"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/jsimonetti/rtnetlink/v2"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const (
	linkFormat = "peer%d"
	milisecond = 1_000_000
)

var (
	ipnl *rtnetlink.Conn
	tcnl *tc.Tc
	dev  *net.Interface

	knownDevs     map[uint32]string
	knownDevsLock sync.RWMutex
	defaultQopt   = tc.NetemQopt{
		Limit: 1000, // default used by iproute2
	}
)

func init() {
	if err := rtnetlink.RegisterDriver(&Macvlan{}); err != nil {
		panic("register link driver macvlan: " + err.Error())
	}
	ipnl = ipnlDial()
	tcnl = tcnlDial()
	dev = defaultInterface()
	knownDevs = ifaceNames()
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

// defaultInterface returns the interface associated with the default route,
// or else panics.
func defaultInterface() *net.Interface {
	routes, err := listIPv4Routes()
	if err != nil {
		panic("detect interface: ip route list: " + err.Error())
	}
	def, err := findDefaultRoute(routes)
	if err != nil {
		panic("detect interface: find default route: " + err.Error())
	}
	iface, err := net.InterfaceByIndex(int(def.Attributes.OutIface))
	if err != nil {
		panic("detect interface: " + err.Error())
	}
	return iface
}

// ifaceNames returns a map associating the known interfaces index with their
// name, or else panics.
func ifaceNames() map[uint32]string {
	devs := make(map[uint32]string)
	ifaces, err := net.Interfaces()
	if err != nil {
		panic("get interface names: " + err.Error())
	}
	for _, iface := range ifaces {
		devs[uint32(iface.Index)] = iface.Name
	}
	return devs
}

// ifaceName gets the name for given interface from known devices.
func ifaceName(index uint32) string {
	knownDevsLock.RLock()
	defer knownDevsLock.RUnlock()
	if name, ok := knownDevs[index]; ok {
		return name
	} else {
		return fmt.Sprint(index)
	}
}

// findDefaultRoute tries to find the current default route.
func findDefaultRoute(routes []rtnetlink.RouteMessage) (*rtnetlink.RouteMessage, error) {
	for _, r := range routes {
		if r.Attributes.Dst == nil {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("default route not found")
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

// listIPv4Routes retrieves only the IPv4 routes from the main routing table.
func listIPv4Routes() ([]rtnetlink.RouteMessage, error) {
	req := &rtnetlink.RouteMessage{
		Family: unix.AF_INET,
		Scope:  unix.RT_SCOPE_UNIVERSE,
		Table:  unix.RT_TABLE_MAIN,
	}
	flags := netlink.Request | netlink.Dump
	msgs, err := ipnl.Execute(req, unix.RTM_GETROUTE, flags)
	routes := make([]rtnetlink.RouteMessage, len(msgs))
	for i := range msgs {
		routes[i] = *msgs[i].(*rtnetlink.RouteMessage)
	}
	return routes, err
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
	routes, err := listIPv4Routes()
	if err != nil {
		return fmt.Errorf("ip route list: %w", err)
	}
	gateway, err := findGateway(routes)
	if err != nil {
		return fmt.Errorf("find gateway: %w", err)
	}

	// prometheus container is launched first and docker increments IP addresses
	prometheus := make(net.IP, 4)
	copy(prometheus, gateway)
	prometheus[3] += 1

	ipnet, err := findIPv4()
	if err != nil {
		return fmt.Errorf("find network: %w", err)
	}
	prefix, _ := ipnet.Mask.Size()
	network := ipnet.IP.Mask(ipnet.Mask)
	routes = slices.DeleteFunc(routes, func(r rtnetlink.RouteMessage) bool {
		return r.Type != unix.RTN_UNICAST
	})
	if err := delRoutes(routes); err != nil {
		return fmt.Errorf("flush routes: %w", err)
	}
	routes = []rtnetlink.RouteMessage{
		{ // Add a route to the gateway
			Scope:     unix.RT_SCOPE_LINK,
			Type:      unix.RTN_UNICAST,
			DstLength: 32,
			Attributes: rtnetlink.RouteAttributes{
				Dst:      gateway,
				OutIface: uint32(dev.Index),
			},
		},
		{ // Add a route to the prometheus container
			Scope:     unix.RT_SCOPE_LINK,
			Type:      unix.RTN_UNICAST,
			DstLength: 32,
			Attributes: rtnetlink.RouteAttributes{
				Dst:      prometheus,
				OutIface: uint32(dev.Index),
			},
		},
		{ // Make all the other address of the current network unreachable
			Type:      unix.RTN_UNREACHABLE,
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
				Data: &Macvlan{
					Mode: MacvlanModeBridge,
				},
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
	knownDevsLock.Lock()
	knownDevs[uint32(link.Index)] = linkName
	knownDevsLock.Unlock()
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
			Parent:  tc.HandleRoot,
		},
		Attribute: tc.Attribute{
			Kind:  "netem",
			Netem: &tc.Netem{Qopt: defaultQopt},
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
	// TODO: send a single message when it is enough
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

// changeQdisc changes the parameters of the interface's netem qdisc corresponding
// to the given health status.
func changeQdisc(health Health) error {
	linkName := fmt.Sprintf(linkFormat, health.Peer.ID)
	link, err := net.InterfaceByName(linkName)
	if err != nil {
		return fmt.Errorf("interface %s: %w", linkName, err)
	}
	var (
		netem   = &tc.Netem{}
		rate    uint32
		rate64  = uint64(health.Bandwidth / 8) // netem expects bytes
		latency = int64(health.Latency * milisecond)
		jitter  = int64(health.Jitter * milisecond)
	)
	if rate64 > math.MaxUint32 {
		netem.Rate64 = &rate64
		rate = math.MaxUint32
	} else {
		rate = uint32(rate64)
	}
	netem.Rate = &tc.NetemRate{Rate: rate}
	netem.Latency64 = &latency
	netem.Jitter64 = &jitter
	netem.Corr = &tc.NetemCorr{
		// We apply 25% correlation on jitter
		Delay: 25,
	}
	netem.Qopt = defaultQopt
	netem.Qopt.Loss = uint32(health.PacketLoss)

	qdisc := tc.Object{
		Msg: tc.Msg{
			Ifindex: uint32(link.Index),
			Parent:  tc.HandleRoot,
		},
		Attribute: tc.Attribute{
			Kind:  "netem",
			Netem: netem,
		},
	}
	return tcnl.Qdisc().Change(&qdisc)
}

// dumpRoutes writes the content of the main routing table to w, with an output
// similar to "ip route show".
func dumpRoutes(w io.Writer) error {
	routes, err := listIPv4Routes()
	routes = slices.DeleteFunc(routes, func(r rtnetlink.RouteMessage) bool {
		return r.Type == unix.RTN_LOCAL || r.Type == unix.RTN_BROADCAST
	})
	if err != nil {
		return fmt.Errorf("list routes: %w", err)
	}
	buf := make([]byte, 0, len(routes)*64)
	buf = fmt.Appendln(buf, "routes:")
	for _, r := range routes {
		var parts []any
		a := &r.Attributes
		switch r.Type {
		case unix.RTN_UNICAST: // Do not print type
		case unix.RTN_BLACKHOLE:
			parts = append(parts, "blackhole")
		case unix.RTN_UNREACHABLE:
			parts = append(parts, "unreachable")
		default:
			parts = append(parts, fmt.Sprintf("type %v", r.Type))
		}
		if a.Dst != nil {
			if r.DstLength != 32 {
				parts = append(parts, fmt.Sprintf("%v/%d", a.Dst, r.DstLength))
			} else {
				parts = append(parts, a.Dst)
			}
		} else {
			parts = append(parts, "default")
		}
		if a.Gateway != nil {
			parts = append(parts, "via", a.Gateway)
		}
		parts = append(parts, "dev", ifaceName(a.OutIface))
		if a.Src != nil {
			parts = append(parts, "src", a.Src)
		}
		buf = fmt.Appendln(buf, parts...)
	}
	_, err = w.Write(buf)
	return err
}

// dumpRoutes writes the list of the current qdiscs to w, with an output similar
// to "tc qdisc show".
func dumpQdiscs(w io.Writer) error {
	qdiscs, err := tcnl.Qdisc().Get()
	if err != nil {
		return fmt.Errorf("list routes: %w", err)
	}
	buf := make([]byte, 0, len(qdiscs)*64)
	buf = fmt.Appendln(buf, "qdiscs:")
	for _, q := range qdiscs {
		var parts []any
		parts = append(parts, "qdisc", q.Kind)
		if q.Handle == tc.HandleRoot {
			parts = append(parts, "0:")
		} else {
			maj, min := core.SplitHandle(q.Handle)
			parts = append(parts, fmt.Sprintf("%d:%d", maj, min))
		}
		parts = append(parts, "dev", ifaceName(q.Ifindex))
		switch q.Kind {
		case "netem":
			parts = append(parts, attrsNetem(q.Netem)...)
		}
		buf = fmt.Appendln(buf, parts...)
	}
	_, err = w.Write(buf)
	return err
}

func attrsNetem(netem *tc.Netem) []any {
	var parts []any
	parts = append(parts, "limit", netem.Qopt.Limit)
	if netem.Latency64 != nil {
		parts = append(parts, "delay", time.Duration(*netem.Latency64))
	}
	if netem.Jitter64 != nil && *netem.Jitter64 != 0 {
		parts = append(parts, "jitter", time.Duration(*netem.Jitter64))
		if netem.Corr != nil && netem.Corr.Delay != 0 {
			delay := (100 * float64(netem.Corr.Delay)) / math.MaxUint32
			parts = append(parts, fmt.Sprintf("%.f%%", delay))
		}
	}
	if netem.Rate != nil && netem.Rate.Rate != 0 {
		var rate uint64 = uint64(netem.Rate.Rate)
		if netem.Rate64 != nil {
			rate = *netem.Rate64
		}
		parts = append(parts, "rate")
		// netem rate is in bytes, we display as bits with SI prefix
		rate *= 8
		if rate > 100_000_000_000 {
			parts = append(parts, fmt.Sprintf("%dGb/s", rate/1_000_000_000))
		} else if rate > 100_000_000 {
			parts = append(parts, fmt.Sprintf("%dMb/s", rate/1_000_000))
		} else {
			parts = append(parts, fmt.Sprintf("%dkb/s", rate/1_000))
		}
	}
	return parts
}
