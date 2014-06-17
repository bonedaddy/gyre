// Package beacon implements a peer-to-peer discovery service for local
// networks. A beacon can broadcast and/or capture service announcements
// using UDP messages on the local area network. This implementation uses
// IPv4 UDP broadcasts. You can define the format of your outgoing beacons,
// and set a filter that validates incoming beacons. Beacons are sent and
// received asynchronously in the background.
//
// This package is an idiomatic go translation of zbeacon class of czmq at
// following address:
//      https://github.com/zeromq/czmq
//
// Instead of ZMQ_PEER socket it uses go channel and also uses go routine
// instead of zthread. To simplify the implementation it doesn't pass API
// calls through the pipe (as zbeacon does) instead it modifies beacon
// struct directly.
//
// For more information please visit:
//		http://hintjens.com/blog:32
//
package beacon

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"time"
)

const (
	beaconMax       = 255
	defaultInterval = 1 * time.Second
)

type Signal struct {
	Addr     string
	Transmit []byte
}

type Beacon struct {
	signals    chan *Signal
	conn       *net.UDPConn  // UDP connection for send/recv
	port       int           // UDP port number we work on
	interval   time.Duration // Beacon broadcast interval
	noecho     bool          // Ignore own (unique) beacons
	terminated bool          // API shut us down
	transmit   []byte        // Beacon transmit data
	filter     []byte        // Beacon filter data
	addr       string        // Our own address
	cast       *net.UDPAddr  // Our broadcast/multicast address
	ticker     <-chan time.Time
}

// Creates a new beacon on a certain UDP port.
func New(port int) (*Beacon, error) {

	var (
		ip    net.IP
		ipnet *net.IPNet
		found bool
		cast  *net.UDPAddr
	)

	ifs, err := net.Interfaces()
	for _, iface := range ifs {
		if iface.Flags&net.FlagLoopback == 0 && (iface.Flags&net.FlagBroadcast != 0 || iface.Flags&net.FlagMulticast != 0) {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			ip, ipnet, _ = net.ParseCIDR(addrs[0].String())

			if iface.Flags&net.FlagMulticast != 0 {
				casts, err := iface.MulticastAddrs()
				if err != nil {
					continue
				}
				cast = &net.UDPAddr{Port: port, IP: net.ParseIP(casts[0].String())}
			} else if iface.Flags&net.FlagBroadcast != 0 {
				bcast := ipnet.IP
				for i := 0; i < len(ipnet.Mask); i++ {
					bcast[i] |= ^ipnet.Mask[i]
				}
				cast = &net.UDPAddr{Port: port, IP: bcast}
			}

			found = true
			break
		}
	}

	if !found {
		return nil, errors.New("no interfaces to bind to")
	}

	conn, err := net.ListenUDP("udp", cast)
	if err != nil {
		return nil, err
	}

	b := &Beacon{
		signals:  make(chan *Signal),
		interval: defaultInterval,
		addr:     ip.String(),
		port:     port,
		conn:     conn,
		cast:     cast,
	}

	go b.listen()
	go b.signal()

	return b, nil
}

// Terminates the beacon.
func (b *Beacon) Close() {
	b.terminated = true
	close(b.signals)
}

// Returns our own IP address as printable string
func (b *Beacon) Addr() string {
	return b.addr
}

// Port returns port number
func (b *Beacon) Port() int {
	return b.port
}

// SetInterval sets broadcast interval.
func (b *Beacon) SetInterval(interval time.Duration) *Beacon {
	b.interval = interval
	return b
}

// NoEcho filters out any beacon that looks exactly like ours.
func (b *Beacon) NoEcho() *Beacon {
	b.noecho = true
	return b
}

// Publish starts broadcasting beacon to peers at the specified interval.
func (b *Beacon) Publish(transmit []byte) *Beacon {
	b.transmit = transmit
	if b.interval == 0 {
		b.ticker = time.After(defaultInterval)
	} else {
		b.ticker = time.After(b.interval)
	}
	return b
}

// Silence stops broadcasting beacon.
func (b *Beacon) Silence() *Beacon {
	b.transmit = nil
	return b
}

// Subscribe starts listening to other peers; zero-sized filter means get everything.
func (b *Beacon) Subscribe(filter []byte) *Beacon {
	b.filter = filter
	return b
}

// Unsubscribe stops listening to other peers.
func (b *Beacon) Unsubscribe() *Beacon {
	b.filter = nil
	return b
}

// Signals returns Signals channel
func (b *Beacon) Signals() chan *Signal {
	return b.signals
}

func (b *Beacon) listen() {
	for {
		buff := make([]byte, beaconMax)
		n, addr, err := b.conn.ReadFromUDP(buff)
		if err != nil || n > beaconMax {
			continue
		}

		send := bytes.HasPrefix(buff[:n], b.filter)
		if send && b.noecho {
			send = !bytes.Equal(buff[:n], b.transmit)
		}

		if send && !b.terminated {
			// Send the arrived signal to the Signals channel
			parts := strings.SplitN(addr.String(), ":", 2)
			ipaddress := parts[0]
			select {
			case b.signals <- &Signal{ipaddress, buff[:n]}:
			default:
			}
		}
	}
}

func (b *Beacon) signal() {
	for {
		select {
		case <-b.ticker:
			if b.terminated {
				break
			}
			if b.transmit != nil {
				// Signal other beacons
				b.conn.WriteToUDP(b.transmit, b.cast)
			}
			b.ticker = time.After(b.interval)
		}
	}
}
