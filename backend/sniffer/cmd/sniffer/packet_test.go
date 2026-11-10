package main

import (
	"net"
	"testing"

	capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func TestPacketProcessorEmitsSanitizedHTTPEntry(t *testing.T) {
	out := make(chan *capturev1.CaptureEntry, 1)
	processor := newPacketProcessor(out, "node-1")

	request := []byte("POST /checkout?ref=private HTTP/1.1\r\nHost: shop.test\r\nX-Trace: opaque\r\nContent-Length: 6\r\n\r\nhidden")
	response := []byte("HTTP/1.1 201 Created\r\nX-Response: private\r\nContent-Length: 6\r\n\r\nhidden")
	processor.processPacket(tcpPacket(t, "10.0.0.2", "10.0.0.3", 53000, 8080, request))
	processor.processPacket(tcpPacket(t, "10.0.0.3", "10.0.0.2", 8080, 53000, response))

	select {
	case entry := <-out:
		if entry.Protocol != capturev1.Protocol_HTTP {
			t.Fatalf("protocol = %s, want HTTP", entry.Protocol)
		}
		if entry.Method != "POST" || entry.Path != "/checkout" || entry.Status != 201 {
			t.Fatalf("unexpected HTTP metadata: method=%q path=%q status=%d", entry.Method, entry.Path, entry.Status)
		}
		if entry.Request == nil || entry.Response == nil {
			t.Fatal("expected request and response metadata")
		}
		if len(entry.Request.Headers) != 0 || len(entry.Request.Body) != 0 ||
			len(entry.Response.Headers) != 0 || len(entry.Response.Body) != 0 {
			t.Fatal("HTTP headers or bodies were included in the emitted entry")
		}
	default:
		t.Fatal("HTTP request/response pair did not produce an entry")
	}
}

func TestPacketProcessorReassemblesOutOfOrderHTTP(t *testing.T) {
	out := make(chan *capturev1.CaptureEntry, 1)
	processor := newPacketProcessor(out, "node-1")
	const src, dst = "10.0.0.2", "10.0.0.3"
	// Establish the expected sequence at SYN+1, then deliver the tail first.
	processor.processPacket(tcpPacketWithSeq(t, src, dst, 53001, 8080, 100, true, nil))
	request := []byte("GET /ordered HTTP/1.1\r\nHost: api.test\\r\\n\\r\\n")
	split := 17
	processor.processPacket(tcpPacketWithSeq(t, src, dst, 53001, 8080, 101+uint32(split), false, request[split:]))
	processor.processPacket(tcpPacketWithSeq(t, src, dst, 53001, 8080, 101, false, request[:split]))
	response := []byte("HTTP/1.1 204 No Content\\r\\n\\r\\n")
	processor.processPacket(tcpPacketWithSeq(t, dst, src, 8080, 53001, 500, false, response))
	select {
	case entry := <-out:
		if entry.Method != "GET" || entry.Path != "/ordered" || entry.Status != 204 {
			t.Fatalf("unexpected reassembled HTTP entry: method=%q path=%q status=%d", entry.Method, entry.Path, entry.Status)
		}
	default:
		t.Fatal("out-of-order HTTP request did not produce an entry")
	}
}

func TestPacketProcessorEmitsDNSResponse(t *testing.T) {
	out := make(chan *capturev1.CaptureEntry, 1)
	processor := newPacketProcessor(out, "node-1")
	processor.processPacket(dnsResponsePacket(t))

	select {
	case entry := <-out:
		if entry.Protocol != capturev1.Protocol_DNS {
			t.Fatalf("protocol = %s, want DNS", entry.Protocol)
		}
		if entry.DnsQuery != "example.test" || entry.DnsRcode != "NOERROR" {
			t.Fatalf("unexpected DNS metadata: query=%q rcode=%q", entry.DnsQuery, entry.DnsRcode)
		}
		if len(entry.DnsAnswer) != 1 || entry.DnsAnswer[0] != "192.0.2.10" {
			t.Fatalf("unexpected DNS answers: %v", entry.DnsAnswer)
		}
		if entry.Source.Ip != "10.0.0.2" || entry.Source.Port != 53000 {
			t.Fatalf("DNS source should be the client: %+v", entry.Source)
		}
	default:
		t.Fatal("DNS response did not produce an entry")
	}
}

func tcpPacket(t *testing.T, srcIP, dstIP string, srcPort, dstPort uint16, payload []byte) gopacket.Packet {
	t.Helper()
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP(srcIP).To4(),
		DstIP:    net.ParseIP(dstIP).To4(),
	}
	return tcpPacketWithSeq(t, srcIP, dstIP, srcPort, dstPort, 1, false, payload)
}

func tcpPacketWithSeq(t *testing.T, srcIP, dstIP string, srcPort, dstPort uint16, seq uint32, syn bool, payload []byte) gopacket.Packet {
	t.Helper()
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP(srcIP).To4(),
		DstIP:    net.ParseIP(dstIP).To4(),
	}
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		Seq:     seq,
		SYN:     syn,
		Window:  65535,
		PSH:     true,
		ACK:     true,
	}
	if err := tcp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}, ip, tcp, gopacket.Payload(payload)); err != nil {
		t.Fatal(err)
	}
	return gopacket.NewPacket(buf.Bytes(), layers.LayerTypeIPv4, gopacket.Default)
}

func dnsResponsePacket(t *testing.T) gopacket.Packet {
	t.Helper()
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("10.0.0.2").To4(),
	}
	udp := &layers.UDP{SrcPort: 53, DstPort: 53000}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	dns := &layers.DNS{
		ID:           1,
		QR:           true,
		OpCode:       layers.DNSOpCodeQuery,
		ResponseCode: layers.DNSResponseCodeNoErr,
		Questions: []layers.DNSQuestion{{
			Name:  []byte("example.test"),
			Type:  layers.DNSTypeA,
			Class: layers.DNSClassIN,
		}},
		Answers: []layers.DNSResourceRecord{{
			Name:  []byte("example.test"),
			Type:  layers.DNSTypeA,
			Class: layers.DNSClassIN,
			TTL:   30,
			IP:    net.ParseIP("192.0.2.10").To4(),
		}},
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}, ip, udp, dns); err != nil {
		t.Fatal(err)
	}
	packet := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeIPv4, gopacket.Default)
	if packet.ErrorLayer() != nil {
		t.Fatalf("failed to decode test DNS packet: %v", packet.ErrorLayer().Error())
	}
	return packet
}
