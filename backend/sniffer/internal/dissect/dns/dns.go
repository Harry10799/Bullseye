// Package dnsdissect extracts DNS queries and responses from gopacket DNS layers.
//
// gopacket already has a full DNS parser (layers.DNS), so this is a thin
// adapter that converts it to Bullseye's Message format.
package dnsdissect

import (
        "strings"

        "github.com/google/gopacket"
        "github.com/google/gopacket/layers"
)

// Message is a dissected DNS message.
type Message struct {
        IsRequest bool
        Query     string
        Type      string
        Rcode     string
        Answer    []string
        TTL       int
}

// Parse extracts a DNS Message from a gopacket Packet, or returns nil if
// the packet doesn't contain a DNS layer.
func Parse(pkt gopacket.Packet) *Message {
        dnsLayer := pkt.Layer(layers.LayerTypeDNS)
        if dnsLayer == nil {
                return nil
        }
        dns, ok := dnsLayer.(*layers.DNS)
        if !ok {
                return nil
        }

        m := &Message{
                IsRequest: dns.QR == false,
        }

        // first question
        if len(dns.Questions) > 0 {
                q := dns.Questions[0]
                m.Query = strings.TrimSuffix(string(q.Name), ".")
                m.Type = q.Type.String()
        }

        // rcode
        m.Rcode = responseCodeName(dns.ResponseCode)

        // answers
        for _, ans := range dns.Answers {
                if ans.IP != nil {
                        m.Answer = append(m.Answer, ans.IP.String())
                } else if len(ans.CNAME) > 0 {
                        m.Answer = append(m.Answer, "CNAME "+string(ans.CNAME))
                } else if len(ans.Name) > 0 {
                        m.Answer = append(m.Answer, string(ans.Name))
                }
                if ans.TTL > 0 && m.TTL == 0 {
                        m.TTL = int(ans.TTL)
                }
        }

        return m
}

func responseCodeName(code layers.DNSResponseCode) string {
        switch code {
        case layers.DNSResponseCodeNoErr:
                return "NOERROR"
        case layers.DNSResponseCodeFormErr:
                return "FORMERR"
        case layers.DNSResponseCodeServFail:
                return "SERVFAIL"
        case layers.DNSResponseCodeNXDomain:
                return "NXDOMAIN"
        case layers.DNSResponseCodeNotImp:
                return "NOTIMP"
        case layers.DNSResponseCodeRefused:
                return "REFUSED"
        default:
                return code.String()
        }
}
