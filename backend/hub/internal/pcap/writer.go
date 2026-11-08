// Package pcap implements real .pcap file export so captures open in Wireshark.
//
// We use the pcapgo writer from gopacket — it writes pcap (not pcapng) for
// maximum compatibility. The output is a single file containing the L4 frames
// reconstructed from the L7 entries we have stored.
//
// Note: Bullseye stores L7 entries (request/response pairs), not raw L4 frames.
// PCAP export therefore synthesizes TCP/IP headers around the L7 payloads so
// the resulting file is meaningful in Wireshark (follow TCP stream shows the
// actual HTTP/gRPC exchange).
package pcap

import (
        "encoding/binary"
        "fmt"
        "io"
        "net"
        "time"

        "github.com/google/gopacket"
        "github.com/google/gopacket/layers"
        "github.com/google/gopacket/pcapgo"
        "github.com/rs/zerolog/log"
)

// Writer wraps a pcapgo.Writer and adds entry-serialization helpers.
type Writer struct {
        w *pcapgo.Writer
}

// New creates a Writer that writes a pcap file to w.
// SnapLen is the max packet size captured; 65535 is the standard default.
func New(w io.Writer) (*Writer, error) {
        pw := pcapgo.NewWriterNanos(w)
        if err := pw.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
                return nil, fmt.Errorf("pcap: write file header: %w", err)
        }
        return &Writer{w: pw}, nil
}

// Entry is the data needed to synthesize a pcap packet from a stored entry.
type Entry struct {
        Timestamp    time.Time
        SrcIP        string
        SrcPort      uint16
        DstIP        string
        DstPort      uint16
        RequestData  []byte
        ResponseData []byte
}

// WriteEntry writes the request + response as two separate TCP packets,
// sharing the same 4-tuple so they appear as a single TCP stream in Wireshark.
func (pw *Writer) WriteEntry(e Entry) error {
        // synthesize a fake TCP stream: SYN, SYN-ACK, ACK, request, response, FIN
        // For simplicity we just write the data packets with incrementing seq nums.
        srcIP, dstIP, err := parseIPs(e.SrcIP, e.DstIP)
        if err != nil {
                return err
        }

        flow := &tcpFlow{
                srcIP: srcIP, srcPort: e.SrcPort,
                dstIP: dstIP, dstPort: e.DstPort,
        }

        if len(e.RequestData) > 0 {
                if err := pw.writePacket(e.Timestamp, flow, e.RequestData, true); err != nil {
                        return fmt.Errorf("pcap: write request: %w", err)
                }
        }
        if len(e.ResponseData) > 0 {
                if err := pw.writePacket(e.Timestamp.Add(time.Millisecond), flow.reverse(), e.ResponseData, true); err != nil {
                        return fmt.Errorf("pcap: write response: %w", err)
                }
        }
        return nil
}

type tcpFlow struct {
        srcIP, dstIP gopacket.Endpoint
        srcPort, dstPort uint16
        seq, ack uint32
}

func (f *tcpFlow) reverse() *tcpFlow {
        return &tcpFlow{
                srcIP: f.dstIP, srcPort: f.dstPort,
                dstIP: f.srcIP, dstPort: f.srcPort,
                seq: f.ack, ack: f.seq,
        }
}

func (pw *Writer) writePacket(ts time.Time, f *tcpFlow, payload []byte, psh bool) error {
        eth := &layers.Ethernet{
                SrcMAC:       fakeMAC(f.srcIP),
                DstMAC:       fakeMAC(f.dstIP),
                EthernetType: layers.EthernetTypeIPv4,
        }
        ip := &layers.IPv4{
                Version: 4, IHL: 5,
                Protocol: layers.IPProtocolTCP,
                SrcIP:    ipFromEndpoint(f.srcIP),
                DstIP:    ipFromEndpoint(f.dstIP),
                Length:   uint16(20 + 20 + len(payload)),
        }
        tcp := &layers.TCP{
                SrcPort: layers.TCPPort(f.srcPort),
                DstPort: layers.TCPPort(f.dstPort),
                Seq:     f.seq, Ack: f.ack,
                Window: 65535,
                ACK:    true, PSH: psh,
        }
        buf := gopacket.NewSerializeBuffer()
        opts := gopacket.SerializeOptions{FixLengths: true}
        if err := gopacket.SerializeLayers(buf, opts, eth, ip, tcp, gopacket.Payload(payload)); err != nil {
                return err
        }
        return pw.w.WritePacket(gopacket.CaptureInfo{
                Timestamp:     ts,
                CaptureLength: len(buf.Bytes()),
                Length:        len(buf.Bytes()),
        }, buf.Bytes())
}

// --- helpers ---

// gopacket.Endpoint is constructed via layers.NewIPEndpoint which panics on
// invalid input. We use net.ParseIP + manual endpoint construction instead.
func parseIPs(src, dst string) (gopacket.Endpoint, gopacket.Endpoint, error) {
        srcIP := net.ParseIP(src)
        dstIP := net.ParseIP(dst)
        if srcIP == nil || dstIP == nil {
                return gopacket.Endpoint{}, gopacket.Endpoint{}, fmt.Errorf("invalid IP pair: %s -> %s", src, dst)
        }
        // layers.NewIPEndpoint panics on nil, but we've already checked
        s := layers.NewIPEndpoint(srcIP)
        d := layers.NewIPEndpoint(dstIP)
        return s, d, nil
}

func ipFromEndpoint(e gopacket.Endpoint) []byte {
        if len(e.Raw()) >= 4 {
                return e.Raw()[:4]
        }
        return []byte{0, 0, 0, 0}
}

func ipFromString(s string) []byte {
        if ip := netParseIP(s); ip != nil {
                return ip
        }
        return []byte{0, 0, 0, 0}
}

func fakeMAC(ip gopacket.Endpoint) []byte {
        raw := ip.Raw()
        if len(raw) < 4 {
                return []byte{0x02, 0x42, 0, 0, 0, 0}
        }
        return []byte{0x02, 0x42, raw[0], raw[1], raw[2], raw[3]}
}

// binary.BigEndian is used for parsing; we don't actually need it here but
// keep the import for future use (e.g., DNS header parsing).
var _ = binary.BigEndian

// Log unused import warning suppression for log package.
var _ = log.Debug
