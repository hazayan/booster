package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var initializedNetworkState struct {
	sync.Mutex
	ifnames []string
}

var wpaSupplicantState struct {
	sync.Mutex
	ifnames []string
}

func rememberInitializedInterface(ifname string) {
	initializedNetworkState.Lock()
	defer initializedNetworkState.Unlock()

	if slices.Contains(initializedNetworkState.ifnames, ifname) {
		return
	}
	initializedNetworkState.ifnames = append(initializedNetworkState.ifnames, ifname)
}

func initializedInterfaces() []string {
	initializedNetworkState.Lock()
	defer initializedNetworkState.Unlock()

	return append([]string(nil), initializedNetworkState.ifnames...)
}

func waitForNetworkReady(timeout time.Duration) error {
	if config.Network == nil {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		links, err := netlink.LinkList()
		if err == nil {
			for _, link := range links {
				attrs := link.Attrs()
				if attrs == nil || attrs.Name == "lo" {
					continue
				}
				if len(config.Network.Interfaces) > 0 && !macListContains(attrs.HardwareAddr, config.Network.Interfaces) {
					continue
				}
				addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
				if err == nil && len(addrs) > 0 {
					info("network ready on %s with %d IPv4 address(es)", attrs.Name, len(addrs))
					return nil
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("network did not become ready within %s", timeout)
}

func parseDNSServers(raw string) ([]net.IP, error) {
	var ips []net.IP
	for server := range strings.SplitSeq(raw, ",") {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		ip := net.ParseIP(server)
		if ip == nil {
			return nil, fmt.Errorf("Unable to parse IP address for DNS server: %v", server)
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func runDhcp(ifname string) error {
	dhcp := client4.NewClient()
	var conversation []*dhcpv4.DHCPv4
	for range 40 {
		var err error
		conversation, err = dhcp.Exchange(ifname)
		if err == nil {
			break
		}
		debug("%s got error from DHCP exchange: %v", ifname, err)
		time.Sleep(time.Second)
	}
	var ack *dhcpv4.DHCPv4
	for _, m := range conversation {
		switch m.MessageType() {
		case dhcpv4.MessageTypeAck:
			ack = m
		}
	}
	if ack == nil {
		return fmt.Errorf("%s: no DHCP ACK received", ifname)
	}

	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return err
	}

	addr := netlink.Addr{IPNet: &net.IPNet{
		IP:   ack.YourIPAddr,
		Mask: ack.SubnetMask(),
	}}
	if err := netlink.AddrAdd(link, &addr); err != nil {
		return err
	}

	gateway := dhcpv4.GetIP(dhcpv4.OptionRouter, ack.Options)
	if gateway != nil {
		defaultRoute := netlink.Route{Gw: gateway}
		if err := netlink.RouteAdd(&defaultRoute); err != nil {
			return err
		}
	}

	dnsServers := dhcpv4.GetIPs(dhcpv4.OptionDomainNameServer, ack.Options)
	if dnsServers != nil {
		if err := writeResolvConf(dnsServers); err != nil {
			return err
		}
	}

	return nil
}

func shutdownNetwork() {
	for _, ifname := range initializedInterfaces() {
		debug("shutting down network interface %s", ifname)
		link, err := netlink.LinkByName(ifname)
		if err != nil {
			continue
		}

		addrs, _ := netlink.AddrList(link, netlink.FAMILY_ALL)
		for _, a := range addrs {
			_ = netlink.AddrDel(link, &a)
		}

		routes, _ := netlink.RouteList(link, netlink.FAMILY_ALL)
		for _, r := range routes {
			_ = netlink.RouteDel(&r)
		}

		_ = netlink.LinkSetDown(link)
	}
}

func rememberWpaSupplicantInterface(ifname string) bool {
	wpaSupplicantState.Lock()
	defer wpaSupplicantState.Unlock()

	if slices.Contains(wpaSupplicantState.ifnames, ifname) {
		return false
	}
	wpaSupplicantState.ifnames = append(wpaSupplicantState.ifnames, ifname)
	return true
}

func startWpaSupplicant(ifname string) error {
	wifi := config.Network.Wifi
	if wifi == nil {
		return nil
	}

	if !rememberWpaSupplicantInterface(ifname) {
		return nil
	}

	wpaSupplicantPath := strings.TrimSpace(wifi.WpaSupplicantPath)
	if wpaSupplicantPath == "" {
		wpaSupplicantPath = "/usr/bin/wpa_supplicant"
	}
	if err := os.MkdirAll("/run/wpa_supplicant", 0o755); err != nil {
		return err
	}

	info("starting wpa_supplicant for Wi-Fi network %q on %s", wifi.SSID, ifname)
	cmd := exec.Command(
		wpaSupplicantPath,
		"-i", ifname,
		"-c", "/etc/wpa_supplicant/booster.conf",
		"-D", "nl80211,wext",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting wpa_supplicant: %w", err)
	}
	return nil
}

func isWirelessInterface(ifname string) bool {
	_, err := os.Stat("/sys/class/net/" + ifname + "/wireless")
	return err == nil
}

func waitForWifiCarrier(ifname string) error {
	if config.Network.Wifi == nil || !isWirelessInterface(ifname) {
		return nil
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		carrier, err := os.ReadFile("/sys/class/net/" + ifname + "/carrier")
		if err == nil && strings.TrimSpace(string(carrier)) == "1" {
			return nil
		}
		operstate, err := os.ReadFile("/sys/class/net/" + ifname + "/operstate")
		if err == nil && strings.TrimSpace(string(operstate)) == "up" {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s: timeout waiting for Wi-Fi association", ifname)
}

func waitForLinkCarrier(ifname string) error {
	if config.Network.Wifi != nil && isWirelessInterface(ifname) {
		return waitForWifiCarrier(ifname)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		carrier, err := os.ReadFile("/sys/class/net/" + ifname + "/carrier")
		if err == nil && strings.TrimSpace(string(carrier)) == "1" {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("%s: timeout waiting for link carrier", ifname)
}

func initializeNetworkInterface(ifname string) error {
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return err
	}
	hardwareAddr := link.Attrs().HardwareAddr
	debug("detected network interface %s (%s)", ifname, hardwareAddr)

	if len(config.Network.Interfaces) > 0 {
		if !macListContains(hardwareAddr, config.Network.Interfaces) {
			info("interface %s (%s) is not in 'active' list, skipping it", ifname, hardwareAddr)
			return nil
		}
	}

	ch := make(chan netlink.LinkUpdate)
	done := make(chan struct{})
	defer close(done)
	if err := netlink.LinkSubscribe(ch, done); err != nil {
		return err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	rememberInitializedInterface(ifname)

	if isWirelessInterface(ifname) {
		if err := startWpaSupplicant(ifname); err != nil {
			return err
		}
	}

	timeout := time.After(20 * time.Second)
	debug("%s waiting interface to be UP", ifname)
linkReadinessLoop:
	for {
		select {
		case ev := <-ch:
			if ifname == ev.Link.Attrs().Name && (ev.IfInfomsg.Flags&unix.IFF_UP != 0) {
				debug("%s: interface is UP", ifname)
				break linkReadinessLoop
			}
		case <-timeout:
			return fmt.Errorf("Unable to setup network link %s: timeout", ifname)
		}
	}

	c := config.Network
	if err := waitForLinkCarrier(ifname); err != nil {
		return err
	}
	if c.Dhcp {
		debug("%s: run DHCP", ifname)
		if err := runDhcp(ifname); err != nil {
			return err
		}
	} else {
		// static address
		if c.IP != "" {
			addr, err := netlink.ParseAddr(c.IP)
			if err != nil {
				return err
			}
			if err := netlink.AddrAdd(link, addr); err != nil {
				return err
			}
		}

		if c.Gateway != "" {
			gw := net.ParseIP(c.Gateway)
			if gw == nil {
				return fmt.Errorf("network.gateway: unable to parse ip address %s", c.Gateway)
			}
			defaultRoute := netlink.Route{Gw: gw}
			if err := netlink.RouteAdd(&defaultRoute); err != nil {
				return err
			}
		}

		if c.DNSServers != "" {
			ips, err := parseDNSServers(c.DNSServers)
			if err != nil {
				return err
			}
			if err := writeResolvConf(ips); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeResolvConf(servers []net.IP) error {
	var resolvConf bytes.Buffer
	for _, ip := range servers {
		resolvConf.WriteString("nameserver ")
		resolvConf.WriteString(ip.String())
		resolvConf.WriteByte('\n')
	}
	resolvConf.WriteString("search .\n")

	return os.WriteFile("/etc/resolv.conf", resolvConf.Bytes(), 0o644)
}
