// SPDX-License-Identifier: Apache-2.0
package reassembly

import (
	"bytes"
	"testing"
	"time"
)

func TestTCPAssemblerReordersSegments(t *testing.T) {
	a := NewTCPAssembler(DefaultTCPConfig())
	key := StreamKey{SrcIP:"192.0.2.1", SrcPort:1234, DstIP:"192.0.2.2", DstPort:80}
	a.Push(key, 100, true, nil) // SYN establishes the initial sequence number.
	if got := a.Push(key, 106, false, []byte("world")); len(got) != 0 { t.Fatalf("out-of-order bytes emitted: %q", got) }
	got := a.Push(key, 101, false, []byte("hello"))
	if !bytes.Equal(got, []byte("helloworld")) { t.Fatalf("reassembled = %q", got) }
}

func TestTCPAssemblerIgnoresRetransmission(t *testing.T) {
	a := NewTCPAssembler(DefaultTCPConfig())
	key := StreamKey{SrcIP:"a", SrcPort:1, DstIP:"b", DstPort:2}
	if got := a.Push(key, 10, false, []byte("abc")); string(got) != "abc" { t.Fatal(string(got)) }
	if got := a.Push(key, 10, false, []byte("abc")); len(got) != 0 { t.Fatalf("duplicate emitted: %q", got) }
}

func TestTCPAssemblerBoundsAndExpiresFlows(t *testing.T) {
	cfg := DefaultTCPConfig()
	cfg.MaxPerFlow = 4
	cfg.MaxBuffered = 4
	cfg.IdleTimeout = time.Millisecond
	a := NewTCPAssembler(cfg)
	key := StreamKey{SrcIP:"a", SrcPort:1, DstIP:"b", DstPort:2}
	a.Push(key, 100, false, []byte("a"))
	a.Push(key, 103, false, []byte("too-large"))
	flows, buffered, dropped := a.Stats()
	if flows != 0 || buffered != 0 || dropped != 1 { t.Fatalf("stats = flows:%d buffered:%d dropped:%d", flows, buffered, dropped) }
	a.Push(key, 200, false, []byte("x"))
	time.Sleep(2*time.Millisecond)
	a.GC()
	flows, buffered, _ = a.Stats()
	if flows != 0 || buffered != 0 { t.Fatalf("idle flow not expired: flows:%d buffered:%d", flows, buffered) }
}

func TestTCPAssemblerUsesSYNSequenceSpace(t *testing.T) {
	a := NewTCPAssembler(DefaultTCPConfig())
	key := StreamKey{SrcIP:"a", SrcPort:1, DstIP:"b", DstPort:2}
	a.Push(key, 100, true, nil)
	got := a.Push(key, 101, false, []byte("data"))
	if string(got) != "data" { t.Fatalf("payload after SYN = %q", got) }
}
