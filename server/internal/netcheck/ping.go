// Package netcheck measures the network (D6): every interval a ping round per target and a DNS
// lookup per server, stored in the rings, and at fixed times of day a speed test against
// Cloudflare, kept as a list of results.
package netcheck

import (
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Lost marks an echo request without a reply in a ping round.
const Lost time.Duration = -1

// Pinger sends count echo requests gap apart to addr and returns each one's round trip, or Lost.
type Pinger interface {
	Ping(ctx context.Context, addr string, count int, gap, timeout time.Duration) ([]time.Duration, error)
}

// ICMP pings through the kernel's unprivileged ICMP socket (SOCK_DGRAM, IPPROTO_ICMP), which needs
// no root and no capability, only a group in net.ipv4.ping_group_range. The kernel sets the echo
// id to the socket's own and hands each socket only its own replies.
type ICMP struct{}

func (ICMP) Ping(ctx context.Context, addr string, count int, gap, timeout time.Duration) ([]time.Duration, error) {
	ip, err := net.DefaultResolver.LookupIP(ctx, "ip4", addr)
	if err != nil || len(ip) == 0 {
		return nil, fmt.Errorf("ping %s: no IPv4 address: %v", addr, err)
	}
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("ping socket (check net.ipv4.ping_group_range): %w", err)
	}
	defer c.Close()
	dst := &net.UDPAddr{IP: ip[0]}

	rtt := make([]time.Duration, count)
	sent := make([]time.Time, count)
	for i := range rtt {
		rtt[i] = Lost
	}
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		got := 0
		for got < count {
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				return // the deadline after the last request, or the socket closed
			}
			at := time.Now()
			m, err := icmp.ParseMessage(1, buf[:n])
			if err != nil || m.Type != ipv4.ICMPTypeEchoReply {
				continue
			}
			e, ok := m.Body.(*icmp.Echo)
			if !ok || e.Seq < 0 || e.Seq >= count {
				continue
			}
			mu.Lock()
			if !sent[e.Seq].IsZero() && rtt[e.Seq] == Lost {
				rtt[e.Seq] = at.Sub(sent[e.Seq])
				got++
			}
			mu.Unlock()
		}
	}()
	var sendErr error
	for i := 0; i < count; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				c.Close()
				<-done
				return nil, ctx.Err()
			case <-time.After(gap):
			}
		}
		b, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: i, Data: []byte("doinspect")}}).Marshal(nil)
		mu.Lock()
		sent[i] = time.Now()
		mu.Unlock()
		if _, err := c.WriteTo(b, dst); err != nil {
			sendErr = err
		}
	}
	c.SetReadDeadline(time.Now().Add(timeout))
	<-done
	mu.Lock()
	defer mu.Unlock()
	if sendErr != nil && Stats(rtt).Loss == 100 {
		return nil, fmt.Errorf("ping %s: %w", addr, sendErr)
	}
	return rtt, nil
}

// Round is what one ping round says: the average round trip and the jitter in milliseconds, NaN
// when no reply came, and the share of requests lost in percent.
type Round struct{ RTT, Jitter, Loss float64 }

// Stats sums up a ping round. Jitter is the average difference between consecutive replies'
// round trips, skipping lost ones, as RFC 3550 counts it for consecutive packets.
func Stats(rtt []time.Duration) Round {
	if len(rtt) == 0 {
		return Round{RTT: math.NaN(), Jitter: math.NaN(), Loss: math.NaN()}
	}
	var got []float64
	for _, d := range rtt {
		if d != Lost {
			got = append(got, float64(d)/float64(time.Millisecond))
		}
	}
	r := Round{RTT: math.NaN(), Jitter: math.NaN(), Loss: 100 * float64(len(rtt)-len(got)) / float64(len(rtt))}
	if len(got) == 0 {
		return r
	}
	sum := 0.0
	for _, v := range got {
		sum += v
	}
	r.RTT = sum / float64(len(got))
	if len(got) > 1 {
		diff := 0.0
		for i := 1; i < len(got); i++ {
			diff += math.Abs(got[i] - got[i-1])
		}
		r.Jitter = diff / float64(len(got)-1)
	} else {
		r.Jitter = 0
	}
	return r
}
