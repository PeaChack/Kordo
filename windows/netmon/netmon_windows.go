//go:build windows

// Package netmon reports network changes (interfaces, routes, addresses)
// through IP Helper notifications, and answers "does the uplink have IPv6".
package netmon

import (
	"net/netip"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Monitor struct {
	mu       sync.Mutex
	handles  []windows.Handle
	onChange func(reason string)
	ignore   func(luid uint64) bool
}

var (
	active   *Monitor
	activeMu sync.Mutex
	cbIface  = windows.NewCallback(func(ctx uintptr, row unsafe.Pointer, typ uint32) uintptr {
		dispatch("interface", row, unsafe.Offsetof(windows.MibIpInterfaceRow{}.InterfaceLuid))
		return 0
	})
	cbRoute = windows.NewCallback(func(ctx uintptr, row unsafe.Pointer, typ uint32) uintptr {
		dispatch("route", row, unsafe.Offsetof(windows.MibIpForwardRow2{}.InterfaceLuid))
		return 0
	})
	cbAddr = windows.NewCallback(func(ctx uintptr, row unsafe.Pointer, typ uint32) uintptr {
		dispatch("address", row, unsafe.Offsetof(windows.MibUnicastIpAddressRow{}.InterfaceLuid))
		return 0
	})
)

// dispatch forwards an event. For interface rows the LUID is at offset 8
// (MIB_IPINTERFACE_ROW: Family uint16 + padding, then InterfaceLuid uint64).
func dispatch(reason string, row unsafe.Pointer, luidOffset uintptr) {
	activeMu.Lock()
	m := active
	activeMu.Unlock()
	if m == nil {
		return
	}
	// Events of our own TUN (its routes and addresses appear on every
	// connect) are ignored, otherwise each connect would reset the engine.
	if row != nil && m.ignore != nil {
		luid := *(*uint64)(unsafe.Add(row, luidOffset))
		if m.ignore(luid) {
			return
		}
	}
	m.onChange(reason)
}

// Start registers for notifications. ignore filters events from our own TUN.
func Start(onChange func(reason string), ignore func(luid uint64) bool) (*Monitor, error) {
	m := &Monitor{onChange: onChange, ignore: ignore}
	activeMu.Lock()
	active = m
	activeMu.Unlock()
	var h windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, cbIface, nil, false, &h); err != nil {
		return nil, err
	}
	m.handles = append(m.handles, h)
	if err := windows.NotifyRouteChange2(windows.AF_UNSPEC, cbRoute, nil, false, &h); err == nil {
		m.handles = append(m.handles, h)
	}
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, cbAddr, nil, false, &h); err == nil {
		m.handles = append(m.handles, h)
	}
	return m, nil
}

func (m *Monitor) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.handles {
		_ = windows.CancelMibChangeNotify2(h)
	}
	m.handles = nil
	activeMu.Lock()
	if active == m {
		active = nil
	}
	activeMu.Unlock()
}

// Adapter is a summary of an up, non-loopback adapter.
type Adapter struct {
	Name     string
	LUID     uint64
	Index    uint32
	Addrs    []netip.Addr
	Gateways []netip.Addr
}

func Adapters() ([]Adapter, error) {
	size := uint32(15000)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_ANYCAST,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil, err
		}
	}
	var out []Adapter
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp || aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		a := Adapter{Name: windows.UTF16PtrToString(aa.FriendlyName), LUID: aa.Luid, Index: aa.IfIndex}
		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			if ip, ok := sockaddrIP(ua.Address); ok {
				a.Addrs = append(a.Addrs, ip)
			}
		}
		for gw := aa.FirstGatewayAddress; gw != nil; gw = gw.Next {
			if ip, ok := sockaddrIP(gw.Address); ok {
				a.Gateways = append(a.Gateways, ip)
			}
		}
		out = append(out, a)
	}
	return out, nil
}

func sockaddrIP(sa windows.SocketAddress) (netip.Addr, bool) {
	if sa.Sockaddr == nil {
		return netip.Addr{}, false
	}
	switch sa.Sockaddr.Addr.Family {
	case windows.AF_INET:
		p := (*windows.RawSockaddrInet4)(unsafe.Pointer(sa.Sockaddr))
		return netip.AddrFrom4(p.Addr), true
	case windows.AF_INET6:
		p := (*windows.RawSockaddrInet6)(unsafe.Pointer(sa.Sockaddr))
		return netip.AddrFrom16(p.Addr), true
	}
	return netip.Addr{}, false
}

// HasGlobalIPv6 reports whether any physical adapter with a gateway has a
// global unicast IPv6 address. skipName excludes our own TUN.
func HasGlobalIPv6(skipName string) bool {
	ads, err := Adapters()
	if err != nil {
		return false
	}
	for _, a := range ads {
		if a.Name == skipName {
			continue
		}
		hasGW6 := false
		for _, g := range a.Gateways {
			if g.Is6() && !g.Is4In6() {
				hasGW6 = true
			}
		}
		if !hasGW6 {
			continue
		}
		for _, ip := range a.Addrs {
			if ip.Is6() && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return true
			}
		}
	}
	return false
}
