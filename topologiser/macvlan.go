// SPDX-FileCopyrightText: 2025 Nicolas Peugnet <nicolas.peugnet@lip6.fr>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"

	"github.com/jsimonetti/rtnetlink/v2"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// MacvlanMode specifies netkit operation mode
type MacvlanMode uint32

func (n MacvlanMode) String() string {
	switch n {
	case 0:
		return "default"
	case MacvlanModePrivate:
		return "private"
	case MacvlanModeVEPA:
		return "vepa"
	case MacvlanModeBridge:
		return "bridge"
	case MacvlanModePassthru:
		return "passthru"
	case MacvlanModeSource:
		return "source"
	default:
		return fmt.Sprintf("unknown MacvlanMode value (%d)", n)
	}
}

// TODO: maybe move these constants to the internal/unix package?
const (
	MACVLAN_MODE_PRIVATE = 1 << iota
	MACVLAN_MODE_VEPA
	MACVLAN_MODE_BRIDGE
	MACVLAN_MODE_PASSTHRU
	MACVLAN_MODE_SOURCE
)

const (
	MacvlanModePrivate  MacvlanMode = MACVLAN_MODE_PRIVATE
	MacvlanModeVEPA     MacvlanMode = MACVLAN_MODE_VEPA
	MacvlanModeBridge   MacvlanMode = MACVLAN_MODE_BRIDGE
	MacvlanModePassthru MacvlanMode = MACVLAN_MODE_PASSTHRU
	MacvlanModeSource   MacvlanMode = MACVLAN_MODE_SOURCE
)

// Macvlan implements LinkDriverVerifier for the netkit driver
type Macvlan struct {
	Mode MacvlanMode // Specifies driver operation mode
}

var _ rtnetlink.LinkDriverVerifier = &Macvlan{}

func (n *Macvlan) New() rtnetlink.LinkDriver {
	return &Macvlan{}
}

func (n *Macvlan) Verify(msg *rtnetlink.LinkMessage) error {
	return nil
}

func (n *Macvlan) Decode(ad *netlink.AttributeDecoder) error {
	for ad.Next() {
		switch ad.Type() {
		case unix.IFLA_MACVLAN_MODE:
			n.Mode = MacvlanMode(ad.Uint32())
		}
	}
	return nil
}

func (n *Macvlan) Encode(ae *netlink.AttributeEncoder) error {
	if n.Mode != 0 {
		ae.Uint32(unix.IFLA_MACVLAN_MODE, uint32(n.Mode))
	}
	return nil
}

func (n *Macvlan) Kind() string {
	return "macvlan"
}
