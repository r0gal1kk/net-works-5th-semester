package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	receiveInterval = 2 * time.Second
	lifeTime        = 5 * time.Second
	checkInterval   = 1 * time.Second
	messagePrefix   = "HELLO "
	localSubnet     = "10.201.187."
)

type PeerTracker struct {
	mu    sync.Mutex
	peers map[string]time.Time
}

func NewPeerTracker() *PeerTracker {
	return &PeerTracker{
		peers: make(map[string]time.Time),
	}
}

func (pt *PeerTracker) Update(addr string) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	_, exists := pt.peers[addr]
	pt.peers[addr] = time.Now()
	return !exists
}

func (pt *PeerTracker) Cleanup() (changed bool, active []string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	now := time.Now()
	for k, t := range pt.peers {
		if now.Sub(t) > lifeTime {
			delete(pt.peers, k)
			changed = true
		}
	}
	for k := range pt.peers {
		active = append(active, k)
	}
	return changed, active
}

func chooseWiFiInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Fatalf("Cannot list interfaces: %v", err)
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			log.Printf("Cannot get addresses for interface %s: %v", i.Name, err)
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				if strings.HasPrefix(ipnet.IP.String(), localSubnet) {
					return &i
				}
			}
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run main.go IP")
		return
	}

	groupAddr := os.Args[1]

	addr, err := net.ResolveUDPAddr("udp", groupAddr)
	if err != nil {
		log.Fatalf("Invalid multicast address: %v", err)
	}

	iface := chooseWiFiInterface()
	if iface == nil {
		log.Fatalf("Cannot find Wi-Fi interface in subnet %s", localSubnet)
	}
	fmt.Printf("Using interface: %s (%d)", iface.Name, iface.Index)

	isIPv6 := addr.IP.To4() == nil

	var conn *net.UDPConn
	if isIPv6 {
		//присоединяем наш wi-fi к  группе
		conn, err = net.ListenMulticastUDP("udp6", iface, addr)
	} else {
		conn, err = net.ListenMulticastUDP("udp4", iface, addr)
	}
	if err != nil {
		log.Fatalf("Failed to join multicast group: %v", err)
	}

	defer func() {
		if err := conn.Close(); err != nil {
			log.Fatalf("Failed to close UDP connection: %v", err)
		}
	}()

	if isIPv6 {
		//обёртываем  net.Udpconn и даём больше возможностей для настройки группы
		p6 := ipv6.NewPacketConn(conn)
		//возможность принимать свои пакеты
		if err := p6.SetMulticastLoopback(true); err != nil {
			log.Fatalf("Cannot enable IPv6 multicast loopback: %v", err)
		}
	} else {
		p4 := ipv4.NewPacketConn(conn)
		if err := p4.SetMulticastLoopback(true); err != nil {
			log.Fatalf("Cannot enable IPv4 multicast loopback: %v", err)
		}
	}
	err = conn.SetReadBuffer(65536)
	if err != nil {
		log.Fatalf("Cannot set read buffer: %v", err)
	}
	tracker := NewPeerTracker()
	pid := os.Getpid()

	nodeID := fmt.Sprintf("%d", pid)

	go func() {
		buf := make([]byte, 1024)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				log.Printf("Read error: %v", err)
				continue
			}
			msg := strings.TrimSpace(string(buf[:n]))
			if strings.HasPrefix(msg, messagePrefix) {
				id := strings.TrimPrefix(msg, messagePrefix)
				key := fmt.Sprintf("%s#%s", src.IP.String(), id)
				added := tracker.Update(key)
				if added {
					_, active := tracker.Cleanup()
					printActive(active)
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(receiveInterval)
		defer ticker.Stop()
		for range ticker.C {
			msg := messagePrefix + nodeID
			_, err := conn.WriteToUDP([]byte(msg), addr)
			if err != nil {
				log.Printf("Send error: %v", err)
			}
		}
	}()

	for range time.Tick(checkInterval) {
		changed, active := tracker.Cleanup()
		if changed {
			printActive(active)
		}
	}
}

func printActive(peers []string) {
	fmt.Println("Active peers:")
	if len(peers) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, p := range peers {
			fmt.Printf("  %s\n", p)
		}
	}
	fmt.Println()
}
