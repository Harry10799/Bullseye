// SPDX-License-Identifier: Apache-2.0
package httpdissect

import "testing"

func TestDissectorParsesHeadersAndContentLengthBody(t *testing.T) {
	d := New()
	request := []byte("POST /checkout HTTP/1.1\r\nHost: shop.test\r\nContent-Length: 6\r\n\r\nhidden")
	msgs, err := d.Feed(request)
	if err != nil { t.Fatalf("Feed(request): %v", err) }
	if len(msgs) != 1 { t.Fatalf("request messages = %d, want 1", len(msgs)) }
	if !msgs[0].IsRequest || msgs[0].Method != "POST" || msgs[0].Path != "/checkout" || string(msgs[0].Body) != "hidden" {
		t.Fatalf("unexpected request: %+v", msgs[0])
	}

	response := []byte("HTTP/1.1 201 Created\r\nContent-Length: 2\r\n\r\nok")
	msgs, err = d.Feed(response)
	if err != nil { t.Fatalf("Feed(response): %v", err) }
	if len(msgs) != 1 || msgs[0].IsRequest || msgs[0].Status != 201 || string(msgs[0].Body) != "ok" {
		t.Fatalf("unexpected response: %+v", msgs)
	}
}
