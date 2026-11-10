package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
	dnsdissect "github.com/bullseye/bullseye/backend/sniffer/internal/dissect/dns"
	"github.com/bullseye/bullseye/backend/sniffer/internal/reassembly"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/rs/zerolog/log"
)

type httpFlow struct {
	client   reassembly.StreamKey
	lastSeen time.Time
}

type packetProcessor struct {
	nodeName   string
	snifferPod string
	out        chan<- *capturev1.CaptureEntry
	http       *reassembly.Tracker
	tcp        *reassembly.TCPAssembler
	flows      map[string]httpFlow
	packets    uint64
	entryID    atomic.Uint64
	entryDrops atomic.Uint64
}

func newPacketProcessor(out chan<- *capturev1.CaptureEntry, nodeName string) *packetProcessor {
	snifferPod := os.Getenv("POD_NAME")
	if snifferPod == "" {
		snifferPod = "bullseye-sniffer-" + nodeName
	}

	return &packetProcessor{
		nodeName:   nodeName,
		snifferPod: snifferPod,
		out:        out,
		http:       reassembly.New(out, snifferPod, nodeName),
		tcp:        reassembly.NewTCPAssembler(reassembly.DefaultTCPConfig()),
		flows:      make(map[string]httpFlow),
	}
}

func (p *packetProcessor) runGC(stop <-chan struct{}) {
	p.http.RunGC(stop)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.tcp.GC()
		}
	}
}

func (p *packetProcessor) processPacket(packet gopacket.Packet) {
	p.packets++
	if p.packets%1024 == 0 {
		p.expireFlows(time.Now())
	}

	p.processDNS(packet)
	p.processHTTP(packet)
}

func (p *packetProcessor) processDNS(packet gopacket.Packet) {
	message := dnsdissect.Parse(packet)
	if message == nil || message.IsRequest || message.Query == "" {
		return
	}

	srcIP, dstIP, ok := packetIPs(packet)
	if !ok {
		return
	}
	srcPort, dstPort, ok := packetPorts(packet)
	if !ok {
		return
	}

	dnsLayer, ok := packet.Layer(layers.LayerTypeDNS).(*layers.DNS)
	if !ok {
		return
	}

	entry := &capturev1.CaptureEntry{
		Id:        p.nextEntryID(),
		Timestamp: time.Now().UnixMilli(),
		Protocol:  capturev1.Protocol_DNS,
		Method:    "QUERY",
		Status:    int32(dnsLayer.ResponseCode),
		DnsQuery:  message.Query,
		DnsType:   message.Type,
		DnsRcode:  message.Rcode,
		DnsAnswer: message.Answer,
		DnsTtl:    int32(message.TTL),
		NodeName:  p.nodeName,
		SnifferPod: p.snifferPod,
		// DNS responses travel from the resolver back to the original client.
		Source:      &capturev1.Endpoint{Ip: dstIP, Port: int32(dstPort)},
		Destination: &capturev1.Endpoint{Ip: srcIP, Port: int32(srcPort)},
	}
	p.send(entry)
}

func (p *packetProcessor) processHTTP(packet gopacket.Packet) {
	tcp, ok := packet.TransportLayer().(*layers.TCP)
	if !ok {
		return
	}

	srcIP, dstIP, ok := packetIPs(packet)
	if !ok {
		return
	}
	key := reassembly.StreamKey{
		SrcIP:   srcIP,
		SrcPort: int(tcp.SrcPort),
		DstIP:   dstIP,
		DstPort: int(tcp.DstPort),
	}
	flowID := httpFlowID(key)
	now := time.Now()
	flow, known := p.flows[flowID]

	if !known && tcp.SYN && !tcp.ACK && isHTTPPort(key.DstPort) {
		flow = httpFlow{client: key, lastSeen: now}
		p.flows[flowID] = flow
		known = true
	}
	// Feed every segment, including SYN, into the sequence-aware assembler.
	// The HTTP parser only sees newly contiguous bytes.
	streamBytes := p.tcp.Push(key, tcp.Seq, tcp.SYN, tcp.Payload)
	if len(streamBytes) == 0 {
		if tcp.FIN || tcp.RST {
			p.tcp.Close(key)
			p.tcp.Close(reverseStreamKey(key))
			delete(p.flows, flowID)
		}
		return
	}

	switch {
	case isHTTPRequest(streamBytes):
		flow = httpFlow{client: key, lastSeen: now}
		p.flows[flowID] = flow
		p.http.FeedHTTP(key, streamBytes, true)
	case isHTTPResponse(streamBytes):
		if !known {
			flow.client = reverseStreamKey(key)
		}
		flow.lastSeen = now
		p.flows[flowID] = flow
		p.http.FeedHTTP(flow.client, streamBytes, false)
	case known:
		flow.lastSeen = now
		p.flows[flowID] = flow
		clientToServer := sameDirection(key, flow.client)
		p.http.FeedHTTP(flow.client, streamBytes, clientToServer)
	default:
		return
	}

	if tcp.FIN || tcp.RST {
		p.tcp.Close(key)
		p.tcp.Close(reverseStreamKey(key))
		delete(p.flows, flowID)
	}
}

func (p *packetProcessor) send(entry *capturev1.CaptureEntry) {
	select {
	case p.out <- entry:
	default:
		dropped := p.entryDrops.Add(1)
		if dropped == 1 || dropped%1000 == 0 {
			log.Warn().Uint64("dropped", dropped).Str("protocol", entry.Protocol.String()).
				Msg("packet processor output queue full; dropping entry")
		}
	}
}

func (p *packetProcessor) nextEntryID() string {
	return fmt.Sprintf("sniffer-%d-%d", time.Now().UnixNano(), p.entryID.Add(1))
}

func (p *packetProcessor) expireFlows(now time.Time) {
	const flowTTL = 5 * time.Minute
	for key, flow := range p.flows {
		if now.Sub(flow.lastSeen) > flowTTL {
			delete(p.flows, key)
		}
	}
}

func packetIPs(packet gopacket.Packet) (string, string, bool) {
	switch network := packet.NetworkLayer().(type) {
	case *layers.IPv4:
		return network.SrcIP.String(), network.DstIP.String(), true
	case *layers.IPv6:
		return network.SrcIP.String(), network.DstIP.String(), true
	default:
		return "", "", false
	}
}

func packetPorts(packet gopacket.Packet) (int, int, bool) {
	switch transport := packet.TransportLayer().(type) {
	case *layers.UDP:
		return int(transport.SrcPort), int(transport.DstPort), true
	case *layers.TCP:
		return int(transport.SrcPort), int(transport.DstPort), true
	default:
		return 0, 0, false
	}
}

func httpFlowID(key reassembly.StreamKey) string {
	src := net.JoinHostPort(key.SrcIP, strconv.Itoa(key.SrcPort))
	dst := net.JoinHostPort(key.DstIP, strconv.Itoa(key.DstPort))
	if src > dst {
		src, dst = dst, src
	}
	return src + "-" + dst
}

func reverseStreamKey(key reassembly.StreamKey) reassembly.StreamKey {
	return reassembly.StreamKey{
		SrcIP:   key.DstIP,
		SrcPort: key.DstPort,
		DstIP:   key.SrcIP,
		DstPort: key.SrcPort,
	}
}

func sameDirection(packet, client reassembly.StreamKey) bool {
	return packet.SrcIP == client.SrcIP &&
		packet.SrcPort == client.SrcPort &&
		packet.DstIP == client.DstIP &&
		packet.DstPort == client.DstPort
}

func isHTTPPort(port int) bool {
	switch port {
	case 80, 3000, 5000, 8000, 8008, 8080, 8081, 8888:
		return true
	default:
		return false
	}
}

func isHTTPRequest(payload []byte) bool {
	for _, method := range []string{"GET ", "POST ", "PUT ", "PATCH ", "DELETE ", "HEAD ", "OPTIONS ", "TRACE ", "CONNECT "} {
		if bytes.HasPrefix(payload, []byte(method)) {
			return true
		}
	}
	return false
}

func isHTTPResponse(payload []byte) bool {
	return bytes.HasPrefix(payload, []byte("HTTP/1."))
}

func processCapturedPackets(ctx context.Context, packets <-chan gopacket.Packet, processor *packetProcessor) error {
	if packets == nil {
		return fmt.Errorf("capture backend does not provide packets")
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case packet, ok := <-packets:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("capture packet stream closed")
			}
			processor.processPacket(packet)
		}
	}
}
